package deploytask

import (
	"encoding/json"
	"testing"
)

const fixture = `{"name":"puppeteer","volumes":{"data":{}},"services":{"server":{"image":"pinned","environment":{"KEEP":"secret"},"volumes":[{"type":"volume","source":"data","target":"/data"}],"ports":[{"target":13306,"published":"13306","protocol":"tcp","host_ip":"127.0.0.1"},{"target":2022,"published":"2022","protocol":"tcp"}]},"web":{"ports":[{"target":8443,"published":"443","protocol":"tcp"}]}}}`

func TestComposeOwnershipAndPreservation(t *testing.T) {
	r := Request{Protocol: "mysql"}
	if _, err := PlanCompose([]byte(fixture), r, nil); err == nil {
		t.Fatal("unmanaged removal accepted")
	}
	owner := &Mapping{Bind: "127.0.0.1", Published: 13306, Target: 13306}
	plan, err := PlanCompose([]byte(fixture), r, owner)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]struct {
			Image       string
			Environment map[string]string
			Ports       []struct{ Target int }
			Volumes     []any
		}
		Volumes map[string]any
	}
	if err = json.Unmarshal(plan.Document, &doc); err != nil {
		t.Fatal(err)
	}
	server := doc.Services["server"]
	if server.Image != "pinned" || server.Environment["KEEP"] != "secret" || server.Environment["NT_DATABASE_PROXY_ADDR"] != "" || len(server.Ports) != 1 || server.Ports[0].Target != 2022 || len(server.Volumes) != 1 || len(doc.Volumes) != 1 || len(doc.Services["web"].Ports) != 1 {
		t.Fatal(string(plan.Document))
	}
	owner.Published = 9999
	if plan.Previous.Published != 13306 {
		t.Fatal("plan aliased caller ownership")
	}
	encoded, _ := json.Marshal(plan)
	var public map[string]any
	json.Unmarshal(encoded, &public)
	if _, ok := public["Document"]; ok {
		t.Fatal("resolved secrets leaked")
	}
}
func TestComposeConflictsAndEnable(t *testing.T) {
	owner := &Mapping{Bind: "127.0.0.1", Published: 13306, Target: 13306}
	for _, port := range []int{443, 2022} {
		if _, err := PlanCompose([]byte(fixture), Request{Protocol: "mysql", Enabled: true, Bind: "0.0.0.0", Port: port}, owner); err == nil {
			t.Fatal("conflict accepted", port)
		}
	}
	plan, err := PlanCompose([]byte(fixture), Request{Protocol: "mysql", Enabled: true, Bind: "0.0.0.0", Port: 23306}, owner)
	if err != nil || plan.Next == nil || plan.Next.Published != 23306 {
		t.Fatal(plan, err)
	}
	if _, err = PlanCompose([]byte(`{"services":{"server":{"network_mode":"host"}}}`), Request{Protocol: "ssh"}, nil); err == nil {
		t.Fatal("host network accepted")
	}
	if _, err = PlanCompose([]byte(fixture), Request{Protocol: "postgres"}, owner); err == nil {
		t.Fatal("wrong ownership accepted")
	}
}
