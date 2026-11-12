//go:build !linux

package application

import (
	"errors"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/listener"
)

func (a *App) managedRDPSpec(v system.ConfigurationValues) (*listener.Spec, error) {
	if v.Bool("native-rdp-enabled") && (v.String("native-rdp-addr") != "" || a.managedRDP != nil && a.managedRDP.Address != "") {
		return nil, errors.New("native recorded RDP requires Linux")
	}
	return nil, nil
}
