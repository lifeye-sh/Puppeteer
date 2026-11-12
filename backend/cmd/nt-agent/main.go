package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"next-terminal/backend/internal/agent"
	"next-terminal/backend/internal/agentupdate"
	"next-terminal/backend/internal/modules/connectivity"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const version = "r07.1-preview"

func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
func readPrivate(path string) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		return nil, errors.New("credential file must be regular, max 16 KiB, mode 0600 or stricter")
	}
	return io.ReadAll(io.LimitReader(f, 16385))
}
func run() error {
	endpoint := flag.String("endpoint", "", "HTTPS backend origin for enrollment")
	configPath := flag.String("config", "agent.json", "private device config file")
	tokenFile := flag.String("token-file", "", "private registration token file (first enrollment only)")
	name := flag.String("name", "", "device name")
	insecure := flag.Bool("allow-insecure-development", false, "allow HTTP only in isolated development")
	supervised := flag.Bool("supervise", false, "run managed Linux updater supervisor")
	healthFile := flag.String("health-file", "", "supervisor health acknowledgement")
	transportFile := flag.String("transport-config", "", "private initial transport settings for enrollment")
	flag.Parse()
	if runtime.GOOS == "windows" {
		return errors.New("Windows ACL installation is not implemented in this preview; use Linux agent")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var c agent.Config
	if *transportFile != "" {
		raw, e := readPrivate(*transportFile)
		if e != nil {
			return e
		}
		if json.Unmarshal(raw, &c) != nil || c.DeviceKey != "" {
			return errors.New("invalid enrollment transport settings")
		}
	}
	if *tokenFile != "" {
		if _, e := os.Stat(*configPath); !os.IsNotExist(e) {
			return errors.New("config exists or cannot be checked; refusing enrollment")
		}
		if *endpoint != "" {
			c.Endpoint = *endpoint
		}
		c.AllowInsecureDevelopment = c.AllowInsecureDevelopment || *insecure
		if _, e := c.URL(); e != nil {
			return e
		}
		raw, e := readPrivate(*tokenFile)
		if e != nil {
			return e
		}
		token := strings.TrimSpace(string(raw))
		if len(token) != 64 {
			return errors.New("invalid token file")
		}
		if *name == "" {
			return errors.New("device name required")
		}
		body, _ := json.Marshal(connectivity.Enrollment{RegistrationToken: token, Name: *name, OS: runtime.GOOS, Arch: runtime.GOARCH, Version: version})
		enrollCtx, done := context.WithTimeout(ctx, 15*time.Second)
		defer done()
		req, _ := http.NewRequestWithContext(enrollCtx, "POST", strings.TrimRight(c.Endpoint, "/")+"/api/agent/v1/enroll", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		client, e := c.Client()
		if e != nil {
			return e
		}
		res, e := client.Do(req)
		if e != nil {
			return errors.New("registration request failed; check token status before retry")
		}
		defer res.Body.Close()
		if res.StatusCode != 201 {
			return fmt.Errorf("registration rejected (HTTP %d)", res.StatusCode)
		}
		var enrolled connectivity.Enrolled
		if e = json.NewDecoder(io.LimitReader(res.Body, 16384)).Decode(&enrolled); e != nil {
			return errors.New("invalid registration response; create a new token")
		}
		if len(enrolled.DeviceKey) != 64 || enrolled.AgentID == "" {
			return errors.New("invalid enrollment identity")
		}
		c.AgentID = enrolled.AgentID
		c.DeviceKey = enrolled.DeviceKey
		b, _ := json.MarshalIndent(c, "", "  ")
		f, e := os.OpenFile(*configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return errors.New("cannot create private config; revoke device and enroll again")
		}
		_, e = f.Write(b)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		log.Print("device registered; private configuration saved")
	} else {
		raw, e := readPrivate(*configPath)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &c); e != nil {
			return errors.New("invalid config")
		}
		if _, e = c.URL(); e != nil {
			return e
		}
	}
	if *supervised {
		return supervise(ctx, c, *configPath)
	}
	c.Managed = *healthFile != ""
	c.Version = version
	backoff := time.Second
	for ctx.Err() == nil {
		if c.PendingKey != "" {
			if e := agent.Rotate(ctx, *configPath, &c); e != nil {
				log.Print("rotation pending; retrying without exposing credentials")
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(5 * time.Second):
				}
				continue
			}
		}
		started := time.Now()
		e := agent.RunConnection(ctx, c, func(epoch int64) {
			if *healthFile != "" {
				_ = agentupdate.WriteJSON(*healthFile, map[string]int64{"epoch": epoch})
			}
			log.Printf("control channel online, epoch %d (scoped SSH dialing enabled)", epoch)
		})
		if errors.Is(e, agent.ErrUpdate) {
			return nil
		}
		if errors.Is(e, agent.ErrRotate) {
			if e = agent.Rotate(ctx, *configPath, &c); e != nil {
				log.Print("rotation pending; retrying")
			}
			continue
		}
		if ctx.Err() != nil {
			break
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		log.Printf("control channel disconnected: %v; retrying", e)
		timer := time.NewTimer(backoff + time.Duration(rand.Int64N(int64(backoff/2)+1)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
	return nil
}
