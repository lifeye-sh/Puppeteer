package nativeclient

import (
	"strings"
	"testing"
)

func TestRecordedDesktopFile(t *testing.T) {
	ticket := "NTICKET:00000000-0000-4000-8000-000000000001:" + strings.Repeat("a", 64)
	raw := "full address:s:127.0.0.1:13389\r\nusername:s:" + ticket + "\r\n"
	clean, err := sanitizeDesktopFile(raw + "remoteapplicationprogram:s:unexpected\r\ndrivestoredirect:s:*\r\n")
	if _, hostErr := sanitizeDesktopFile(strings.Replace(raw, "127.0.0.1", "backend.localhost", 1)); hostErr != nil {
		t.Fatal(hostErr)
	}
	if err != nil || strings.Contains(string(clean), "unexpected") || strings.Contains(string(clean), "direct:s:*") || !strings.Contains(string(clean), "authentication level:i:1") {
		t.Fatal("unsafe desktop settings")
	}
	for _, bad := range []string{raw + raw, strings.Replace(raw, "127.0.0.1", "192.0.2.2", 1), strings.Replace(raw, "13389", "65536", 1), strings.Replace(raw, "NTICKET", "INVALID", 1)} {
		if _, err := sanitizeDesktopFile(bad); err == nil {
			t.Fatal("invalid descriptor accepted")
		}
	}
}
