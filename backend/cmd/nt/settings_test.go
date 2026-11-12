package main

import (
	"next-terminal/backend/internal/modules/system"
	"strings"
	"testing"
)

func TestConfigurationDeltaDoesNotReuseHistoricalChanges(t *testing.T) {
	before := system.ConfigurationValues{"access-log-saved-limit-days": 31, "system-name": "demo"}
	if got := configurationDelta(before, before); len(got) != 0 {
		t.Fatal(got)
	}
	after := system.ConfigurationValues{"access-log-saved-limit-days": 32, "system-name": "demo"}
	if got := configurationDelta(before, after); len(got) != 1 || got[0] != "access-log-saved-limit-days" {
		t.Fatal(got)
	}
}

func TestSettingsPatchInput(t *testing.T) {
	for _, input := range []string{`{}`, `{"set":{}}`, `{"set":{"login-lock-enabled":false},"unknown":1}`, `{"set":{"system-name":"x"}} {}`, strings.Repeat("x", 16385)} {
		if _, err := readSettingsPatch(strings.NewReader(input)); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	patch, err := readSettingsPatch(strings.NewReader(`{"set":{"login-lock-enabled":false,"login-session-duration":480}}`))
	if err != nil || string(patch["login-lock-enabled"]) != "false" {
		t.Fatal(patch, err)
	}
}

func TestSettingsPatchSizeBoundSupportsLongConfiguration(t *testing.T) {
	valid := `{"set":{"llm-shell-prompt":"` + strings.Repeat("x", 32768) + `","mail-ca-pem":"` + strings.Repeat("x", 65536) + `"}}`
	if _, e := readSettingsPatch(strings.NewReader(valid)); e != nil {
		t.Fatal(e)
	}
	oversized := `{"set":{"llm-shell-prompt":"` + strings.Repeat("x", 512*1024) + `"}}`
	if _, e := readSettingsPatch(strings.NewReader(oversized)); e == nil || !strings.Contains(e.Error(), "512 KiB") {
		t.Fatal("size limit not enforced", e)
	}
}

func TestRetentionPreviewPatch(t *testing.T) {
	for _, raw := range []string{`0`, `-2`, `3651`, `null`, `true`, `1.5`, `"1"`} {
		patch, e := readSettingsPatch(strings.NewReader(`{"set":{"login-log-saved-limit-days":` + raw + `}}`))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = retentionPatch(patch); e == nil {
			t.Fatal("invalid retention accepted", raw)
		}
	}
	for _, raw := range []string{`-1`, `1`, `3650`} {
		patch, e := readSettingsPatch(strings.NewReader(`{"set":{"login-log-saved-limit-days":` + raw + `}}`))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = retentionPatch(patch); e != nil {
			t.Fatal(e)
		}
	}
	patch, _ := readSettingsPatch(strings.NewReader(`{"set":{"system-name":"x","login-log-saved-limit-days":1}}`))
	if _, e := retentionPatch(patch); e == nil {
		t.Fatal("non-retention key accepted")
	}
}
