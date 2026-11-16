package deploytask

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestResourceDatabaseBudgetAcrossWALAndProfileChanges(t *testing.T) {
	var fixture map[string]any
	_ = json.Unmarshal(resourceFixture(t), &fixture)
	fixture["services"].(map[string]any)["postgres"].(map[string]any)["environment"] = map[string]string{"POSTGRES_USER": "puppeteer", "POSTGRES_DB": "puppeteer"}
	p, err := planResources(jsonValue(fixture), Request{Protocol: "resources", Enabled: true, ResourceProfile: "light"})
	if err != nil {
		t.Fatal(err)
	}
	d := HostDriver{StateDirectory: t.TempDir(), Executor: ComposeExecutor{Project: "fixture"}}
	p, err = d.walPlan(context.Background(), p.Document, Request{Protocol: "wal", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(p.Document), "archive_mode=on") || !strings.Contains(string(p.Document), "shared_buffers=128MB") {
		t.Fatal(string(p.Document))
	}
	p, err = planResources(p.Document, Request{Protocol: "resources", Enabled: true, ResourceProfile: "standard"})
	if err != nil {
		t.Fatal(err)
	}
	p, err = d.walPlan(context.Background(), p.Document, Request{Protocol: "wal"})
	if err != nil {
		t.Fatal(err)
	}
	var m composeModel
	_ = json.Unmarshal(p.Document, &m)
	command := string(m.Services["postgres"]["command"])
	if strings.Contains(command, "archive_mode=on") || !strings.Contains(command, "shared_buffers=256MB") || strings.Contains(command, "128MB") {
		t.Fatal(command)
	}
}
func TestResourceDatabaseDoesNotOverwriteCustomSettings(t *testing.T) {
	for _, raw := range []string{`["sh","-c","postgres"]`, `["postgres","-c","work_mem=8MB"]`, `["postgres","--config-file=/custom"]`} {
		if _, err := databaseCommand([]byte(raw), false); err == nil {
			t.Fatal(raw)
		}
	}
}
