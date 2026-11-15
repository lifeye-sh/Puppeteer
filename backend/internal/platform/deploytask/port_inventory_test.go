package deploytask

import (
	"context"
	"testing"
)

func TestPublishedPortsFromOtherProjects(t *testing.T) {
	raw := []byte(`{"3306/tcp":[{"HostIp":"0.0.0.0","HostPort":"23306"}]}
{"53/udp":[{"HostIp":"0.0.0.0","HostPort":"53"}]}`)
	if checkPublishedPorts(raw, "127.0.0.1", 23306) == nil {
		t.Fatal("NAT-only conflict missed")
	}
	if err := checkPublishedPorts(raw, "127.0.0.1", 53); err != nil {
		t.Fatal(err)
	}
	if checkPublishedPorts([]byte("invalid"), "127.0.0.1", 23306) == nil {
		t.Fatal("invalid inventory accepted")
	}
	e := fixtureExecutor(t, `case "$*" in
 "container ls --format {{.ID}}") echo abcdef123456 ;;
 "inspect --type container --format {{json .NetworkSettings.Ports}} abcdef123456") echo '{"3306/tcp":[{"HostIp":"127.0.0.1","HostPort":"23306"}]}' ;;
 *) exit 8 ;;
 esac
`)
	if e.CheckPublishedPort(context.Background(), "127.0.0.1", 23306) == nil {
		t.Fatal("external container ignored")
	}
	if err := e.CheckPublishedPort(context.Background(), "127.0.0.2", 23306); err != nil {
		t.Fatal(err)
	}
}
