package listener

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"
)

// HTTP serves one accepted connection for its whole lifetime, including an
// upgraded WebSocket. Switching the accepting socket does not close this peer.
func HTTP(handler http.Handler, tlsConfig *tls.Config, management bool) Handler {
	return func(ctx context.Context, raw net.Conn) {
		peer := &httpPeer{Conn: raw, done: make(chan struct{})}
		defer peer.Close()
		stop := context.AfterFunc(ctx, func() { _ = peer.Close() })
		defer stop()
		var conn net.Conn = peer
		if tlsConfig != nil {
			conn = tls.Server(peer, tlsConfig)
		}
		l := &httpListener{peer: peer, conn: conn}
		s := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024,
			BaseContext: func(net.Listener) context.Context { return ctx },
		}
		if management {
			s.ReadTimeout = 70 * time.Second
			s.WriteTimeout = 75 * time.Second
			s.MaxHeaderBytes = 16 * 1024
		}
		_ = s.Serve(l)
	}
}

type httpPeer struct {
	net.Conn
	done chan struct{}
	once sync.Once
}

func (c *httpPeer) Close() error {
	var err error
	c.once.Do(func() { err = c.Conn.Close(); close(c.done) })
	return err
}

type httpListener struct {
	peer     *httpPeer
	conn     net.Conn
	accepted bool
}

func (l *httpListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		return l.conn, nil
	}
	<-l.peer.done
	return nil, net.ErrClosed
}
func (l *httpListener) Close() error   { return l.peer.Close() }
func (l *httpListener) Addr() net.Addr { return l.peer.LocalAddr() }
