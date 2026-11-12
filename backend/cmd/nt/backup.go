package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"flag"
	"fmt"
	"io"
	"next-terminal/backend/internal/application"
	"next-terminal/backend/internal/modules/backup"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/platform/deployment"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Offline commands do not open the application database or trigger migrations.
func backupCommand(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: nt backup keygen|inspect|pack|stage")
	}
	f := flag.NewFlagSet("backup "+args[0], flag.ContinueOnError)
	switch args[0] {
	case "verify-recipient":
		challenge := f.String("challenge-file", "", "base64 challenge downloaded from backup settings")
		keyFile := f.String("identity-file", "", "offline private identity file")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if *challenge == "" || *keyFile == "" || f.NArg() != 0 {
			return errors.New("challenge-file and identity-file required")
		}
		read := func(path string) ([]byte, error) {
			file, e := os.Open(path)
			if e != nil {
				return nil, e
			}
			defer file.Close()
			raw, e := io.ReadAll(io.LimitReader(file, 8193))
			if e != nil || len(raw) > 8192 {
				return nil, errors.New("verification input exceeds limit")
			}
			return raw, nil
		}
		raw, e := read(*keyFile)
		if e != nil {
			return e
		}
		defer clear(raw)
		key, e := age.ParseX25519Identity(strings.TrimSpace(string(raw)))
		if e != nil {
			return errors.New("invalid offline identity")
		}
		input, e := read(*challenge)
		if e != nil {
			return e
		}
		encrypted, e := base64.StdEncoding.DecodeString(strings.TrimSpace(string(input)))
		if e != nil {
			return errors.New("invalid challenge encoding")
		}
		plain, e := age.Decrypt(bytes.NewReader(encrypted), key)
		if e != nil {
			return errors.New("identity does not match recovery recipient")
		}
		response, e := io.ReadAll(io.LimitReader(plain, 129))
		if e != nil || !regexp.MustCompile(`^[a-f0-9]{64}$`).Match(response) {
			return errors.New("invalid recovery challenge")
		}
		fmt.Println(string(response))
		return nil
	case "create":
		output := f.String("output", "", "new encrypted archive path")
		recipient := f.String("recipient", "", "offline age public recipient")
		scope := f.String("scope", "control-plane", "control-plane or full")
		if e := f.Parse(args[1:]); e != nil {
			return e
		}
		if *output == "" || *recipient == "" || f.NArg() != 0 {
			return errors.New("output and recipient required")
		}
		if e := deployment.LoadSecrets(); e != nil {
			return e
		}
		profile, e := runtimeprofile.FromEnvironment()
		if e != nil {
			return e
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		fence, e := application.FenceOffline(ctx, profile.DatabaseURL)
		if e != nil {
			return e
		}
		defer fence.Close(context.Background())
		parent := filepath.Dir(*output)
		if e = os.MkdirAll(parent, 0700); e != nil {
			return e
		}
		m, e := backup.CreateSnapshot(ctx, profile.DatabaseURL, parent, *output, *recipient, identity.NewToken(), application.Release, *scope, backup.RecoveryBundle{Version: 1, MFAKey: profile.MFAKey, VaultKey: profile.VaultKey})
		if e != nil {
			return e
		}
		return json.NewEncoder(os.Stdout).Encode(m)
	case "pack":
		dir := f.String("directory", "", "private prepared input directory")
		manifest := f.String("manifest", "", "public manifest JSON")
		recipient := f.String("recipient", "", "age X25519 public recipient")
		output := f.String("output", "", "new .ntbackup.age path")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" || *manifest == "" || *recipient == "" || *output == "" || f.NArg() != 0 {
			return errors.New("directory, manifest, recipient and output required")
		}
		file, err := os.Open(*manifest)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(file, backup.MaxManifest+1))
		file.Close()
		if err != nil {
			return err
		}
		if len(raw) > backup.MaxManifest {
			return errors.New("manifest too large")
		}
		var m backup.Manifest
		if err = json.Unmarshal(raw, &m); err != nil {
			return errors.New("invalid manifest")
		}
		m, err = backup.PackDirectory(*dir, *output, *recipient, m)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(m)
	case "stage":
		archive := f.String("archive", "", "trusted encrypted archive")
		identity := f.String("identity-file", "", "offline private key file")
		parent := f.String("parent", "", "private restore workspace parent")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *archive == "" || *identity == "" || *parent == "" || f.NArg() != 0 {
			return errors.New("archive, identity-file and parent required")
		}
		keyFile, err := os.Open(*identity)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(io.LimitReader(keyFile, 4097))
		keyFile.Close()
		if err != nil {
			return err
		}
		if len(raw) > 4096 {
			return errors.New("identity file too large")
		}
		file, err := os.Open(*archive)
		if err != nil {
			return err
		}
		defer file.Close()
		m, dir, err := backup.Stage(file, strings.TrimSpace(string(raw)), backup.SupportedSchema, *parent)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"directory": dir, "manifest": m, "state": "validated_only"})
	case "keygen":
		output := f.String("output", "", "new offline private key file (never overwritten)")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *output == "" || f.NArg() != 0 {
			return errors.New("--output required")
		}
		key, err := age.GenerateX25519Identity()
		if err != nil {
			return err
		}
		file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, err = io.WriteString(file, key.String()+"\n")
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Println(key.Recipient().String())
		return nil
	case "inspect":
		archive := f.String("archive", "", "encrypted .ntbackup.age archive")
		identity := f.String("identity-file", "", "offline recovery identity file")
		maxSchema := f.Int("max-schema", backup.SupportedSchema, "maximum supported schema")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if *archive == "" || *identity == "" || f.NArg() != 0 {
			return errors.New("--archive and --identity-file required")
		}
		keyFile, err := os.Open(*identity)
		if err != nil {
			return err
		}
		key, err := io.ReadAll(io.LimitReader(keyFile, 4097))
		keyFile.Close()
		if err != nil {
			return err
		}
		if len(key) > 4096 {
			return errors.New("identity file too large")
		}
		file, err := os.Open(*archive)
		if err != nil {
			return err
		}
		defer file.Close()
		m, err := backup.InspectArchive(file, strings.TrimSpace(string(key)), *maxSchema)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(m)
	default:
		return errors.New("unknown backup command")
	}
}
