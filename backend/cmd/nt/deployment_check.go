package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"net"
	"puppeteer/backend/internal/application"
	"puppeteer/backend/internal/platform/deployment"
	"puppeteer/backend/internal/platform/deploytask"
	"puppeteer/backend/internal/platform/guac"
	"puppeteer/backend/internal/platform/mp4probe"
	"puppeteer/backend/internal/platform/runtimeprofile"
	"os"
	"strconv"
	"time"
)

// Runs inside the existing server container. Never migrates or seeds a database.
func deploymentCheck(args []string) error {
	return deploymentProbe(args, false)
}
func deploymentProbe(args []string, verify bool) error { return deploymentExecute(args, verify, false) }
func deploymentExecute(args []string, verify, applySite bool) error {
	if len(args) != 4 && len(args) != 8 {
		return errors.New("expected protocol, enabled, bind and port")
	}
	enabled, err := strconv.ParseBool(args[1])
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(args[3])
	if err != nil {
		return err
	}
	request := deploytask.Request{Protocol: args[0], Enabled: enabled, Bind: args[2], Port: port}
	if len(args) == 8 {
		request.SiteID = args[4]
		request.SiteVersion, err = strconv.ParseInt(args[5], 10, 64)
		if err != nil {
			return err
		}
		request.SiteEnabled, err = strconv.ParseBool(args[6])
		if err != nil {
			return err
		}
		request.Actor = args[7]
	}
	if err = deploytask.Validate(request); err != nil {
		return err
	}
	if err = deployment.LoadSecrets(); err != nil {
		return errors.New("deployment secrets unavailable")
	}
	if err = runtimeprofile.Apply(); err != nil {
		return errors.New("runtime profile unavailable")
	}
	if applySite || request.Protocol == "mp4" {
		profile, profileErr := runtimeprofile.FromEnvironment()
		if profileErr != nil || !profile.WritesEnabled {
			return errors.New("deployment changes are blocked during restore quarantine")
		}
	}
	key, _ := hex.DecodeString(os.Getenv("NT_MFA_KEY"))
	vault, _ := hex.DecodeString(os.Getenv("NT_VAULT_KEY"))
	defer clear(key)
	defer clear(vault)
	if len(key) != 32 || len(vault) != 32 {
		return errors.New("deployment keyring unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(os.Getenv("NT_DATABASE_URL"))
	if err != nil {
		return errors.New("database configuration unavailable")
	}
	config.MaxConns = 2
	if !applySite {
		config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return errors.New("database unavailable")
	}
	defer pool.Close()
	app := application.New(pool, application.Config{EncryptionKey: key, VaultKey: vault, DatabaseProxyName: os.Getenv("NT_DATABASE_PROXY_NAME")})
	if applySite {
		err = app.ApplyDeploymentWebsite(ctx, request)
	} else {
		err = app.DeploymentApplicationCheck(ctx, request)
	}
	if err == nil && request.Protocol == "management" && !verify {
		_, currentPort, _ := net.SplitHostPort(os.Getenv("NT_ADDR"))
		if currentPort != strconv.Itoa(request.Port) {
			listener, bindErr := (&net.ListenConfig{}).Listen(ctx, "tcp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(request.Port)))
			if bindErr != nil {
				err = errors.New("管理目标端口在容器内已被占用")
			} else {
				listener.Close()
			}
		}
	}
	if err == nil && verify && request.Protocol == "graphics" {
		if request.Enabled {
			if os.Getenv("NT_GUACD_ADDR") != "guacd:4822" || os.Getenv("NT_GUACD_RELAY_HOST") != "server" {
				err = errors.New("图形组件的应用接入地址未生效")
			} else {
				err = guac.Probe(ctx, "guacd:4822")
			}
		} else {
			if os.Getenv("NT_GUACD_ADDR") != "" || os.Getenv("NT_GUACD_RELAY_HOST") != "" {
				err = errors.New("图形新连接入口尚未关闭")
			}
			conn, dialErr := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", "guacd:4822")
			if conn != nil {
				conn.Close()
			}
			if dialErr == nil {
				err = errors.New("图形组件仍可连接")
			}
		}
	}
	if err == nil && verify && request.Protocol == "rdp-drive" {
		if (os.Getenv("NT_RDP_DRIVE_ENABLED") == "true") != request.Enabled {
			err = errors.New("共享盘的应用接入状态尚未生效")
		}
		if err == nil && request.Enabled {
			err = guac.Probe(ctx, os.Getenv("NT_GUACD_ADDR"))
		}
	}
	if err == nil && request.Protocol == "mp4" {
		if request.Enabled {
			err = checkMP4Cache(ctx)
		}
		if err == nil && verify {
			if request.Enabled {
				if os.Getenv("NT_MP4_URL") != "http://mp4-renderer:8090" {
					err = errors.New("MP4 接入地址尚未生效")
				} else {
					err = mp4probe.Ready(ctx, os.Getenv("NT_MP4_URL"), os.Getenv("NT_MP4_TOKEN"))
				}
			} else {
				if os.Getenv("NT_MP4_URL") != "" {
					err = errors.New("MP4 新任务入口尚未关闭")
				}
				conn, dialErr := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", "mp4-renderer:8090")
				if conn != nil {
					conn.Close()
				}
				if dialErr == nil {
					err = errors.New("MP4 渲染器仍可连接")
				}
			}
		}
	}
	if err == nil && verify && request.Protocol != "graphics" && request.Protocol != "rdp-drive" && request.Protocol != "mp4" {
		target, _ := deploytask.ContainerPort(request.Protocol)
		if request.Protocol == "management" {
			target = request.Port
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			conn, dialErr := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(target)))
			if conn != nil {
				conn.Close()
			}
			if (dialErr == nil) == request.Enabled {
				break
			}
			if time.Now().After(deadline) {
				err = errors.New("实际容器监听状态与部署请求不一致")
				break
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
			if err != nil {
				break
			}
		}
	}
	result := map[string]any{"passed": err == nil}
	if err != nil {
		result["error"] = err.Error()
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}

func managementHealthURL(address string) (string, error) {
	if address == "" {
		address = "127.0.0.1:8089"
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", err
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return "", errors.New("invalid management port")
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	if net.ParseIP(host) == nil {
		return "", errors.New("management health requires an IP address")
	}
	return fmt.Sprintf("http://%s/readyz", net.JoinHostPort(host, port)), nil
}
