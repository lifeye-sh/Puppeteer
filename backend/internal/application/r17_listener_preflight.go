package application

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"puppeteer/backend/internal/modules/system"
	"puppeteer/backend/internal/platform/fault"
	"puppeteer/backend/internal/platform/listener"
)

// listenerPreflight reserves and releases candidate sockets without publishing
// configuration. This checks process-local binding only, not host reachability.
func (a *App) listenerPreflight(w http.ResponseWriter, r *http.Request) error {
	if _, err := a.require(r, "identity.manage"); err != nil {
		return err
	}
	expected, err := configMatch(r, true)
	if err != nil {
		return err
	}
	var body struct {
		Set map[string]json.RawMessage `json:"set"`
	}
	if err = decode(w, r, &body); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, prefix := range []string{"ssh-server", "db-proxy", "postgres-proxy", "website-server", "native-rdp"} {
		allowed[prefix+"-enabled"] = true
		allowed[prefix+"-addr"] = true
	}
	allowed["http-server-addr"] = true
	allowed["native-rdp-public-address"] = true
	a.configurationMu.Lock()
	defer a.configurationMu.Unlock()
	revision, err := a.system.Configuration(r.Context(), 0)
	if err != nil {
		return err
	}
	if revision.ID != *expected {
		return fault.Conflict
	}
	raw, err := json.Marshal(revision.Values)
	if err != nil {
		return err
	}
	var values map[string]json.RawMessage
	if err = json.Unmarshal(raw, &values); err != nil {
		return err
	}
	for key, value := range body.Set {
		if !allowed[key] {
			return fault.Invalid
		}
		values[key] = value
	}
	raw, err = json.Marshal(values)
	if err != nil {
		return err
	}
	var candidate system.ConfigurationValues
	if err = json.Unmarshal(raw, &candidate); err != nil {
		return fault.Invalid
	}
	if err = system.ValidateConfiguration(candidate); err != nil {
		return err
	}
	type check struct {
		Name   string `json:"name"`
		Passed bool   `json:"passed"`
		Reason string `json:"reason,omitempty"`
	}
	checks := []check{}
	var prepared []*listener.Prepared
	defer func() {
		for _, p := range prepared {
			p.Abort()
		}
	}()
	probe := func(name string, m *listener.Manager, specs []listener.Spec) {
		if m == nil {
			checks = append(checks, check{name, false, "监听管理器尚未就绪"})
			return
		}
		p, e := m.Prepare(r.Context(), specs)
		if e != nil {
			checks = append(checks, check{name, false, e.Error()})
			return
		}
		prepared = append(prepared, p)
		checks = append(checks, check{Name: name, Passed: true})
	}
	specs := []listener.Spec{}
	for _, name := range []string{"ssh", "mysql", "postgres"} {
		specs = append(specs, listener.Spec{Name: name, Address: a.requestedListenerAddress(candidate, name), Handle: func(_ context.Context, c net.Conn) { c.Close() }})
	}
	if rdp, e := a.managedRDPSpec(candidate); e != nil {
		checks = append(checks, check{"原生 RDP", false, e.Error()})
	} else if rdp != nil {
		specs = append(specs, *rdp)
	}
	probe("SSH / 数据库 / RDP 进程端口", a.listenerManager, specs)
	probe("管理 / 网站进程端口", a.publicListeners, a.publicListenerSpecs(candidate))
	passed := true
	for _, c := range checks {
		passed = passed && c.Passed
	}
	writeJSON(w, 200, map[string]any{"passed": passed, "checks": checks, "scope": "process-listeners", "notice": "仅验证当前进程监听端口；证书、容器映射、防火墙及外部可达性尚未验证。保存时仍会重新检查。"})
	return nil
}
