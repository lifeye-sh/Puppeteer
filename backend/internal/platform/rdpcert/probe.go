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

func Probe(ctx context.Context, c net.Conn, host string) (Certificate, error) {
	var out Certificate
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
	if _, e := io.ReadFull(c, header); e != nil {
		return out, e
	}
	n := int(binary.BigEndian.Uint16(header[2:]))
	if header[0] != 3 || header[1] != 0 || n < 19 || n > 4096 {
		return out, fmt.Errorf("invalid RDP negotiation header")
	}
	body := make([]byte, n-4)
	if _, e := io.ReadFull(c, body); e != nil {
		return out, e
	}
	if int(body[0])+1 != len(body) || body[1] != 0xd0 || body[6] != 0 || body[7] != 2 || binary.LittleEndian.Uint16(body[9:11]) != 8 {
		return out, fmt.Errorf("RDP TLS negotiation rejected")
	}
	protocol := binary.LittleEndian.Uint32(body[11:15])
	if protocol != 1 && protocol != 2 {
		return out, fmt.Errorf("RDP TLS unavailable")
	}
	// Discovery intentionally accepts a self-signed certificate to display/pin it.
	// This does not change validation for the actual guacd connection.
	conn := tls.Client(c, &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, InsecureSkipVerify: true})
	if e := conn.HandshakeContext(ctx); e != nil {
		return out, e
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return out, fmt.Errorf("missing RDP certificate")
	}
	cert := certs[0]
	digest := sha256.Sum256(cert.Raw)
	return Certificate{Fingerprint: fmt.Sprintf("sha256:%x", digest), Subject: cert.Subject.String(), Issuer: cert.Issuer.String(), NotBefore: cert.NotBefore, NotAfter: cert.NotAfter}, nil
}
