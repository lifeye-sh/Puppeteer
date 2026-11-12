package main

import (
	"context"
	"encoding/hex"
	"log/slog"

	"next-terminal/backend/internal/application"
	"next-terminal/backend/internal/platform/runtimeprofile"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	p, e := application.Open(ctx)
	cancel()
	if e != nil {
		slog.Error("startup failed", "error", e)
		os.Exit(1)
	}
	defer p.Close()
	origins := os.Getenv("NT_ALLOWED_ORIGINS")
	if origins == "" {
		origins = "http://backend.localhost:5174"
	}
	addr := os.Getenv("NT_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8089"
	}
	key, err := hex.DecodeString(os.Getenv("NT_MFA_KEY"))
	if err != nil || len(key) != 32 {
		slog.Error("NT_MFA_KEY must contain 32 random bytes as hex")
		os.Exit(1)
	}
	vaultKey, _ := hex.DecodeString(os.Getenv("NT_VAULT_KEY"))
	websitePort := 443
	if value := os.Getenv("NT_WEBSITE_PUBLIC_PORT"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 65535 {
			slog.Error("invalid website public port")
			os.Exit(1)
		}
		websitePort = parsed
	}
	profile, err := runtimeprofile.FromEnvironment()
	if err != nil {
		slog.Error("runtime profile unavailable")
		os.Exit(1)
	}
	localEntitlement, entitlementVerifier, err := loadEntitlements()
	if err != nil {
		slog.Error("invalid entitlement deployment configuration")
		os.Exit(1)
	}
	app := application.New(p, application.Config{SecureCookies: os.Getenv("NT_ENV") == "production", EntitlementLocal: localEntitlement, EntitlementVerifier: entitlementVerifier, NativeSSHAddr: os.Getenv("NT_NATIVE_SSH_ADDR"), NativeSSHName: os.Getenv("NT_NATIVE_SSH_NAME"), PasskeyRPID: os.Getenv("NT_PASSKEY_RP_ID"), PasskeyOrigins: strings.Split(os.Getenv("NT_PASSKEY_ORIGINS"), ","), DatabaseProxyAddr: os.Getenv("NT_DATABASE_PROXY_ADDR"), PostgresProxyAddr: os.Getenv("NT_POSTGRES_PROXY_ADDR"), DatabaseProxyName: os.Getenv("NT_DATABASE_PROXY_NAME"), MP4URL: os.Getenv("NT_MP4_URL"), MP4Token: os.Getenv("NT_MP4_TOKEN"), MP4Directory: os.Getenv("NT_MP4_DIRECTORY"), RestoreDirectory: os.Getenv("NT_RESTORE_DIRECTORY"), RestoreID: profile.RestoreID, StartFrozen: !profile.WritesEnabled, BackupDirectory: os.Getenv("NT_BACKUP_DIRECTORY"), DatabaseURL: os.Getenv("NT_DATABASE_URL"), WebsitePublicPort: websitePort, RDPDriveEnabled: os.Getenv("NT_RDP_DRIVE_ENABLED") == "true", GuacdAddr: os.Getenv("NT_GUACD_ADDR"), GuacdRelayHost: os.Getenv("NT_GUACD_RELAY_HOST"), AllowedOrigins: strings.Split(origins, ","), EncryptionKey: key, VaultKey: vaultKey, CaptchaRequired: true})
	life, stopLife := context.WithCancel(context.Background())
	defer stopLife()
	if err = configureRecordedRDP(app); err != nil {
		slog.Error("native RDP configuration failed", "error", err)
		os.Exit(1)
	}
	if profile.WritesEnabled {
		recoverCtx, recoverCancel := context.WithTimeout(life, 10*time.Second)
		err = app.RecoverFiles(recoverCtx)
		recoverCancel()
		if err != nil {
			slog.Error("file recovery failed", "error", err)
			os.Exit(1)
		}
	}
	if err = app.StartPublicListeners(life, addr, os.Getenv("NT_WEBSITE_ADDR")); err != nil {
		slog.Error("management listener startup failed", "error", err)
		os.Exit(1)
	}
	defer app.ClosePublicListeners()
	app.StartOperationalServices(life)
	slog.Info("management API listening", "addr", addr)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	stopLife()
	drain, done := context.WithTimeout(context.Background(), 8*time.Second)
	defer done()
	app.StopSessions(drain)
}
