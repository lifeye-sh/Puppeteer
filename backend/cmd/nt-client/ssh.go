package main

import (
	"context"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"next-terminal/backend/internal/nativeclient"
	"os"
	"time"
)

func runSSH(ctx context.Context, client *nativeclient.Client, token nativeclient.Token, asset, command string) error {
	remote, e := client.ConnectSSH(ctx, token, asset)
	if e != nil {
		return e
	}
	defer remote.Close()
	session, e := remote.NewSession()
	if e != nil {
		return e
	}
	defer session.Close()
	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	if command != "" {
		return session.Run(command)
	}
	input, output := int(os.Stdin.Fd()), int(os.Stdout.Fd())
	terminal := term.IsTerminal(input) && term.IsTerminal(output)
	cols, rows := 80, 24
	if terminal {
		if c, r, e := term.GetSize(output); e == nil {
			cols, rows = c, r
		}
		cols, rows = min(500, max(2, cols)), min(300, max(2, rows))
		if e = session.RequestPty("xterm-256color", rows, cols, ssh.TerminalModes{ssh.ECHO: 1}); e != nil {
			return e
		}
		old, e := term.MakeRaw(input)
		if e != nil {
			return e
		}
		defer term.Restore(input, old)
	}
	if e = session.Shell(); e != nil {
		return e
	}
	watch, stop := context.WithCancel(ctx)
	defer stop()
	if terminal {
		go func() {
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				select {
				case <-watch.Done():
					return
				case <-tick.C:
					c, r, e := term.GetSize(output)
					if e == nil {
						c, r = min(500, max(2, c)), min(300, max(2, r))
						if c != cols || r != rows {
							if session.WindowChange(r, c) != nil {
								return
							}
							cols, rows = c, r
						}
					}
				}
			}
		}()
	}
	return session.Wait()
}
