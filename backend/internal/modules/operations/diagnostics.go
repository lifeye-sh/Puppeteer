package operations

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"next-terminal/backend/internal/platform/fault"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var ErrPingUnavailable = errors.New("unprivileged ICMP unavailable")

// Resolve once and retain the selected address for the complete diagnostic.
// No caller-supplied shell commands, port ranges, broadcast or metadata targets.
func DiagnosticIP(ctx context.Context, host string) (net.IP, error) {
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, fault.Invalid
	}
	for _, a := range addresses {
		if a.Zone != "" || a.IP.IsUnspecified() || a.IP.IsMulticast() || a.IP.IsLinkLocalUnicast() || a.IP.IsLinkLocalMulticast() || a.IP.Equal(net.IPv4bcast) {
			return nil, fault.Denied
		}
	}
	return addresses[0].IP, nil
}

// Ping uses datagram ICMP, never a privileged raw socket or external executable.
// Random payload, source and sequence checks reject unrelated/stale replies.
func Ping(ctx context.Context, ip net.IP, sequence int) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	network, bind, protocol := "udp4", "0.0.0.0", 1
	var request, reply icmp.Type = ipv4.ICMPTypeEcho, ipv4.ICMPTypeEchoReply
	if ip.To4() == nil {
		network, bind, protocol = "udp6", "::", 58
		request, reply = ipv6.ICMPTypeEchoRequest, ipv6.ICMPTypeEchoReply
	}
	c, err := icmp.ListenPacket(network, bind)
	if err != nil {
		return ErrPingUnavailable
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = c.SetDeadline(deadline); err != nil {
		return err
	}
	payload := make([]byte, 32)
	if _, err = rand.Read(payload); err != nil {
		return err
	}
	message := icmp.Message{Type: request, Code: 0, Body: &icmp.Echo{ID: os.Getpid() & 0xffff, Seq: sequence, Data: payload}}
	data, err := message.Marshal(nil)
	if err != nil {
		return err
	}
	if _, err = c.WriteTo(data, &net.UDPAddr{IP: ip}); err != nil {
		return err
	}
	buffer := make([]byte, 1500)
	for {
		n, source, e := c.ReadFrom(buffer)
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return e
		}
		peer, ok := source.(*net.UDPAddr)
		if !ok || !peer.IP.Equal(ip) {
			continue
		}
		received, e := icmp.ParseMessage(protocol, buffer[:n])
		if e != nil || received.Type != reply || received.Code != 0 {
			continue
		}
		echo, ok := received.Body.(*icmp.Echo)
		// Linux ping sockets rewrite the identifier; payload is our unpredictable nonce.
		if ok && echo.Seq == sequence && bytes.Equal(echo.Data, payload) {
			return nil
		}
	}
}
