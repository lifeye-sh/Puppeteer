package deploytask

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Docker may publish through NAT without a listening host process. Inspect its
// network-only inventory as well as probing the host socket. Never read container
// environment or infer permission to modify another container from this inventory.
func (e ComposeExecutor) CheckPublishedPort(ctx context.Context, bind string, port int) error {
	raw, err := e.execute(ctx, 15*time.Second, "host port inventory", "container", "ls", "--format", "{{.ID}}")
	if err != nil {
		return err
	}
	ids := strings.Fields(string(raw))
	if len(ids) > 4096 {
		return errors.New("host container inventory exceeds preflight limit")
	}
	valid := regexp.MustCompile(`^[0-9a-f]{12,64}$`)
	for _, id := range ids {
		if !valid.MatchString(id) {
			return errors.New("invalid host container inventory")
		}
	}
	for offset := 0; offset < len(ids); offset += 128 {
		end := min(offset+128, len(ids))
		args := append([]string{"inspect", "--type", "container", "--format", "{{json .NetworkSettings.Ports}}"}, ids[offset:end]...)
		raw, err = e.execute(ctx, 15*time.Second, "host published ports", args...)
		if err != nil {
			return err
		}
		if err = checkPublishedPorts(raw, bind, port); err != nil {
			return err
		}
	}
	return nil
}
func checkPublishedPorts(raw []byte, bind string, port int) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		var ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		}
		err := decoder.Decode(&ports)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("invalid Docker published port inventory")
		}
		for protocol, bindings := range ports {
			if !strings.HasSuffix(protocol, "/tcp") {
				continue
			}
			for _, p := range bindings {
				value, err := strconv.Atoi(p.HostPort)
				if err != nil {
					return errors.New("invalid Docker published port")
				}
				if value == port && (p.HostIP == "" || p.HostIP == "0.0.0.0" || p.HostIP == "::" || bind == "0.0.0.0" || p.HostIP == bind) {
					return errors.New("host TCP port is already published by a Docker container")
				}
			}
		}
	}
}
