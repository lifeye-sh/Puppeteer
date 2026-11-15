package main

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"puppeteer/backend/internal/application"
	"puppeteer/backend/internal/modules/backup"
	"puppeteer/backend/internal/platform/deployment"
	"puppeteer/backend/internal/platform/mp4probe"
	"puppeteer/backend/internal/platform/runtimeprofile"
	"os"
	"time"
)

func deploymentMP4(command string) error {
	if err := deployment.LoadSecrets(); err != nil {
		return err
	}
	if err := runtimeprofile.Apply(); err != nil {
		return err
	}
	profile, err := runtimeprofile.FromEnvironment()
	if err != nil || !profile.WritesEnabled {
		return errors.New("恢复隔离期间不能变更 MP4 组件")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if command == "deployment-mp4-sample" {
		return mp4probe.Sample(ctx, os.Getenv("NT_MP4_URL"), os.Getenv("NT_MP4_TOKEN"))
	}
	pool, err := pgxpool.New(ctx, os.Getenv("NT_DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	key, _ := hex.DecodeString(os.Getenv("NT_MFA_KEY"))
	vault, _ := hex.DecodeString(os.Getenv("NT_VAULT_KEY"))
	defer clear(key)
	defer clear(vault)
	if len(key) != 32 || len(vault) != 32 {
		return errors.New("MP4 队列维护密钥不可用")
	}
	return application.New(pool, application.Config{EncryptionKey: key, VaultKey: vault}).PauseRecordingExports(ctx)
}

func checkMP4Cache(ctx context.Context) error {
	if len(os.Getenv("NT_MP4_TOKEN")) < 32 || len(os.Getenv("NT_MP4_TOKEN")) > 4096 {
		return errors.New("MP4 通信密钥长度必须为 32–4096 字符")
	}
	path := os.Getenv("NT_MP4_DIRECTORY")
	if path != "/var/lib/puppeteer/mp4" {
		return errors.New("MP4 缓存目录配置不兼容")
	}
	// No file creation during preflight. The parent is used before the first run.
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		path = "/var/lib/puppeteer"
	}
	if err := backup.RequireSpace(ctx, path, 512<<20); err != nil {
		return errors.New("MP4 缓存目录不可访问或剩余空间不足 512 MiB")
	}
	return nil
}
