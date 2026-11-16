package resourcebudget

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

func cgroupPressure(current, limit string) error {
	if strings.TrimSpace(limit) == "max" {
		return nil
	}
	used, e1 := strconv.ParseUint(strings.TrimSpace(current), 10, 64)
	maximum, e2 := strconv.ParseUint(strings.TrimSpace(limit), 10, 64)
	if e1 != nil || e2 != nil || maximum == 0 {
		return errors.New("resource_probe_unavailable")
	}
	// v1 uses an enormous sentinel for unlimited. Real resource profiles are GiB.
	if maximum >= 1<<60 {
		return nil
	}
	if used >= maximum || maximum-used < maximum/10 {
		return errors.New("export_memory_pressure")
	}
	return nil
}

func containerMemoryPressure() error {
	for _, pair := range [][2]string{
		{"/sys/fs/cgroup/memory.current", "/sys/fs/cgroup/memory.max"},
		{"/sys/fs/cgroup/memory/memory.usage_in_bytes", "/sys/fs/cgroup/memory/memory.limit_in_bytes"},
	} {
		limit, err := os.ReadFile(pair[1])
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return errors.New("resource_probe_unavailable")
		}
		used, err := os.ReadFile(pair[0])
		if err != nil {
			return errors.New("resource_probe_unavailable")
		}
		return cgroupPressure(string(used), string(limit))
	}
	return nil // Non-container hosts use the system availability check.
}
