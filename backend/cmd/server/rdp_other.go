//go:build !linux

package main

import (
	"errors"
	"next-terminal/backend/internal/application"
	"os"
)

func configureRecordedRDP(_ *application.App) error {
	if os.Getenv("NT_NATIVE_RDP_LOOPBACK") != "" {
		return errors.New("native recorded RDP requires Linux")
	}
	return nil
}
