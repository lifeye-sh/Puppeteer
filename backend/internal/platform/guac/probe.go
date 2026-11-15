package guac

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"time"
)

// Probe checks both installed protocol plugins without connecting to an asset
// or supplying any credentials. A TCP accept alone is not a guacd handshake.
func Probe(ctx context.Context, address string) error {
	for _, protocol := range []string{"rdp", "vnc"} {
		if err := probeProtocol(ctx, address, protocol); err != nil {
			return err
		}
	}
	return nil
}
func probeProtocol(ctx context.Context, address, protocol string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return errors.New("图形组件不可连接")
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	c.SetDeadline(deadline)
	if _, err = io.WriteString(c, Encode("select", protocol)); err != nil {
		return errors.New("图形协议握手写入失败")
	}
	args, err := NewReader(c).Read()
	if err != nil || len(args) < 3 || args[0] != "args" || !slices.Contains(args[1:], "hostname") || !slices.Contains(args[1:], "port") {
		return errors.New("图形组件未通过 RDP/VNC 插件握手检查")
	}
	return nil
}
