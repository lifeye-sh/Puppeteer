//go:build linux

package application

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/listener"
	"os"
	"path/filepath"
	"time"
)

func (a *App) managedRDPSpec(v system.ConfigurationValues) (*listener.Spec, error) {
	if !v.Bool("native-rdp-enabled") {
		return nil, nil
	}
	options := a.managedRDP
	address := v.String("native-rdp-addr")
	if options == nil {
		if address != "" {
			return nil, errors.New("native RDP deployment is unavailable")
		}
		return nil, nil
	}
	if address == "" {
		address = options.Address
	}
	if address == "" {
		return nil, nil
	}
	target, e := net.ResolveTCPAddr("tcp", address)
	if e != nil || target.IP == nil || (!options.ContainerProfile && !target.IP.IsLoopback()) {
		return nil, errors.New("native RDP requires a loopback listener outside the local container profile")
	}
	for _, p := range []string{options.Worker, options.FFmpeg, options.Certificate, options.PrivateKey} {
		if !filepath.IsAbs(p) {
			return nil, errors.New("absolute RDP paths required")
		}
	}
	for _, p := range []string{options.Worker, options.FFmpeg} {
		s, e := os.Stat(p)
		if e != nil || !s.Mode().IsRegular() || s.Mode().Perm()&0111 == 0 {
			return nil, errors.New("native RDP executable unavailable")
		}
	}
	read := func(p string) ([]byte, error) {
		s, e := os.Stat(p)
		if e != nil {
			return nil, e
		}
		if !s.Mode().IsRegular() || s.Size() > 65536 {
			return nil, errors.New("invalid RDP certificate size")
		}
		return os.ReadFile(p)
	}
	certificate, e := read(options.Certificate)
	if e != nil {
		return nil, e
	}
	key, e := read(options.PrivateKey)
	if e != nil {
		return nil, e
	}
	pair, e := tls.X509KeyPair(certificate, key)
	if e != nil {
		return nil, e
	}
	leaf, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return nil, e
	}
	endpoint := v.String("native-rdp-public-address")
	if endpoint == "" {
		endpoint = options.Advertised
	}
	if endpoint == "" {
		endpoint = address
	}
	host, _, e := net.SplitHostPort(endpoint)
	if e != nil || leaf.VerifyHostname(host) != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
		return nil, errors.New("RDP certificate does not cover the advertised endpoint")
	}
	identity := fmt.Sprintf("%x", sha256.Sum256(certificate))
	return &listener.Spec{Name: "rdp", Address: address, Identity: identity, Handle: func(ctx context.Context, raw net.Conn) {
		conn, ok := raw.(*net.TCPConn)
		if !ok {
			return
		}
		if _, e := a.appliedConfiguration(ctx); e != nil {
			return
		}
		select {
		case a.managedRDPSlots <- struct{}{}:
			defer func() { <-a.managedRDPSlots }()
		default:
			return
		}
		a.serveRecordedPeerCertificate(ctx, conn, options.Worker, options.Certificate, options.PrivateKey, options.FFmpeg, certificate, key)
	}}, nil
}
