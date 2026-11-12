package application

import (
	"context"
	"errors"
	"net"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/listener"
)

// The management listener outlives maintenance workers: backups and isolated
// restore must never remove the API needed to observe or recover the instance.
// Bootstrap always uses the deployment address, so a bad persisted port can be
// diagnosed and retried after restart. A successful revision then replaces it.
func (a *App) StartPublicListeners(ctx context.Context, address, websiteAddress string) error {
	a.configurationMu.Lock()
	defer a.configurationMu.Unlock()
	if a.publicListeners != nil || address == "" {
		return errors.New("public listeners already running or missing management address")
	}
	a.publicHTTPAddress, a.publicWebsiteAddress = address, websiteAddress
	a.publicHTTPHandler, a.publicWebsiteHandler = a.Handler(), a.WebsiteHandler()
	m := listener.New(ctx, 512)
	p, err := m.Prepare(ctx, []listener.Spec{{Name: "http", Address: address, Handle: listener.HTTP(a.publicHTTPHandler, nil, true)}})
	if err != nil {
		m.Close()
		return err
	}
	defer p.Abort()
	if err = p.Commit(); err != nil {
		m.Close()
		return err
	}
	a.publicListeners = m
	return nil
}

func (a *App) publicListenerSpecs(v system.ConfigurationValues) []listener.Spec {
	address := v.String("http-server-addr")
	if address == "" {
		address = a.publicHTTPAddress
	}
	specs := []listener.Spec{{Name: "http", Address: address, Handle: listener.HTTP(a.publicHTTPHandler, nil, true)}}
	website := v.String("website-server-addr")
	if website == "" {
		website = a.publicWebsiteAddress
	}
	if v.Bool("website-server-enabled") && website != "" {
		serve := listener.HTTP(a.publicWebsiteHandler, a.WebsiteTLSConfig(), false)
		specs = append(specs, listener.Spec{Name: "website", Address: website, Identity: "website-sni", Handle: func(ctx context.Context, raw net.Conn) {
			if _, e := a.appliedConfiguration(ctx); e != nil {
				return
			}
			serve(ctx, raw)
		}})
	}
	return specs
}

func (a *App) ClosePublicListeners() {
	a.configurationMu.Lock()
	m := a.publicListeners
	a.configurationMu.Unlock()
	if m != nil {
		m.Close()
		m.Wait()
	}
}
