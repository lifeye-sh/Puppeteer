// Package rdpcert reads the certificate presented after RDP security negotiation.
// It never sends credentials or enters a desktop session.
package rdpcert

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"time"
)

type Certificate struct {
	Fingerprint string    `json:"fingerprint"`
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
}

// ProbeError preserves the failed stage without exposing credentials or traffic.
type ProbeError struct {
	Stage string
	Err   error
}

func (e *ProbeError) Error() string { return e.Stage + ": " + e.Err.Error() }
func (e *ProbeError) Unwrap() error { return e.Err }

type NegotiationFailure struct{ Code uint32 }

func (e *NegotiationFailure) Error() string {
	return fmt.Sprintf("RDP negotiation failure 0x%08x", e.Code)
}

func Probe(ctx context.Context, c net.Conn, host string) (out Certificate, err error) {
	stage := "negotiation_write"
	defer func() {
		if err != nil {
			err = &ProbeError{Stage: stage, Err: err}
		}
	}()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(deadline)
	}
	// TPKT, X.224 connection request, RDP_NEG_REQ: TLS + CredSSP/NLA.
	request := []byte{3, 0, 0, 19, 14, 0xe0, 0, 0, 0, 0, 0, 1, 0, 8, 0, 3, 0, 0, 0}
	if _, e := c.Write(request); e != nil {
		return out, e
	}
	header := make([]byte, 4)
	stage = "negotiation_read"
	if _, e := io.ReadFull(c, header); e != nil {
		return out, e
	}
	n := int(binary.BigEndian.Uint16(header[2:]))
	if header[0] != 3 || header[1] != 0 || n < 11 || n > 4096 {
		return out, fmt.Errorf("invalid RDP negotiation header")
	}
	body := make([]byte, n-4)
	if _, e := io.ReadFull(c, body); e != nil {
		return out, e
	}
	stage = "negotiation_response"
	if int(body[0])+1 != len(body) || body[1] != 0xd0 || body[6] != 0 {
		return out, fmt.Errorf("RDP TLS negotiation rejected")
	}
	if len(body) == 7 {
		return out, fmt.Errorf("server selected standard RDP security without TLS")
	}
	if len(body) != 15 || binary.LittleEndian.Uint16(body[9:11]) != 8 {
		return out, fmt.Errorf("invalid RDP negotiation response")
	}
	if body[7] == 3 {
		return out, &NegotiationFailure{Code: binary.LittleEndian.Uint32(body[11:15])}
	}
	if body[7] != 2 {
		return out, fmt.Errorf("invalid RDP negotiation response type")
	}
	protocol := binary.LittleEndian.Uint32(body[11:15])
	if protocol != 1 && protocol != 2 {
		return out, fmt.Errorf("RDP TLS unavailable")
	}
	// Discovery intentionally accepts a self-signed certificate to display/pin it.
	// This does not change validation for the actual guacd connection.
	conn := tls.Client(c, discoveryTLSConfig(host))
	stage = "tls_handshake"
	if e := conn.HandshakeContext(ctx); e != nil {
		return out, e
	}
	certs := conn.ConnectionState().PeerCertificates
	stage = "certificate"
	if len(certs) == 0 {
		return out, fmt.Errorf("missing RDP certificate")
	}
	cert := certs[0]
	digest := sha256.Sum256(cert.Raw)
	return Certificate{Fingerprint: fmt.Sprintf("sha256:%x", digest), Subject: cert.Subject.String(), Issuer: cert.Issuer.String(), NotBefore: cert.NotBefore, NotAfter: cert.NotAfter}, nil
}

func discoveryTLSConfig(host string) *tls.Config {
	// Windows-generated RDP certificates can permit key encipherment but not
	// digital signatures. These cannot negotiate ECDHE_RSA. Go no longer offers
	// static RSA by default. Enable only AES-GCM RSA suites for this credential-
	// free discovery connection, never globally or on application TLS listeners.
	suites := []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, InsecureSkipVerify: true, CipherSuites: suites}
}
