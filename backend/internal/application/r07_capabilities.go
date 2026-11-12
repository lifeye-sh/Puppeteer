package application

// These flags describe implementation/configuration, never user authorization
// or the health of a particular remote host. Handlers still enforce policy.
func (a *App) protocolCapabilities() map[string]map[string]bool {
	graph := a.config.GuacdAddr != "" && a.config.GuacdRelayHost != ""
	return map[string]map[string]bool{
		"ssh":    {"connect": true, "recording": true, "observe": true, "share": true, "files": true, "clipboard": false, "resize": true},
		"telnet": {"connect": true, "recording": true, "observe": true, "share": true, "files": false, "clipboard": false, "resize": false},
		"rdp":    {"connect": graph, "recording": graph, "observe": graph, "share": graph, "files": graph && a.config.RDPDriveEnabled, "clipboard": graph, "resize": graph},
		"vnc":    {"connect": graph, "recording": graph, "observe": graph, "share": graph, "files": false, "clipboard": false, "resize": false},
	}
}
