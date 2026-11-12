//go:build linux

package main

import (
	"next-terminal/backend/internal/application"
	"os"
)

func configureRecordedRDP(a *application.App) error {
	addr := os.Getenv("NT_NATIVE_RDP_LOOPBACK")
	options := application.NativeRDPOptions{Address: addr, Worker: os.Getenv("NT_NATIVE_RDP_WORKER"), Certificate: os.Getenv("NT_NATIVE_RDP_CERT"), PrivateKey: os.Getenv("NT_NATIVE_RDP_KEY"), FFmpeg: os.Getenv("NT_NATIVE_RDP_FFMPEG")}
	if os.Getenv("NT_NATIVE_RDP_HOST_PROFILE") == "windows-local" {
		options.Address = "0.0.0.0:13389"
		options.Advertised = "backend.localhost:13389"
		options.ContainerProfile = true
	}
	if options.Address == "" {
		return nil
	}
	a.ConfigureNativeRDP(options)
	return nil
}
