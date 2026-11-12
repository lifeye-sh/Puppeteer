package agent

import (
	"context"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
	psnet "github.com/shirou/gopsutil/v4/net"
	"next-terminal/backend/internal/modules/connectivity/agentwire"
	"regexp"
	"time"
)

func collectMetrics(ctx context.Context, paths []string, pattern string) agentwire.Metrics {
	v := agentwire.Metrics{CollectedAt: time.Now().UTC(), Disks: []agentwire.DiskMetric{}, Networks: []agentwire.NetworkMetric{}, Errors: []string{}}
	if values, e := cpu.PercentWithContext(ctx, 0, false); e == nil && len(values) == 1 {
		v.CPU = &values[0]
	} else {
		v.Errors = append(v.Errors, "cpu unavailable")
	}
	if m, e := mem.VirtualMemoryWithContext(ctx); e == nil {
		v.MemoryTotal = m.Total
		v.MemoryUsed = m.Used
	} else {
		v.Errors = append(v.Errors, "memory unavailable")
	}
	if len(paths) == 0 {
		paths = []string{"/"}
	}
	for _, path := range paths {
		if d, e := disk.UsageWithContext(ctx, path); e == nil {
			v.Disks = append(v.Disks, agentwire.DiskMetric{Path: path, Total: d.Total, Used: d.Used})
		} else {
			v.Errors = append(v.Errors, "disk unavailable")
		}
	}
	re, e := regexp.Compile(pattern)
	if e != nil {
		v.Errors = append(v.Errors, "invalid network filter")
		return v
	}
	if n, e := psnet.IOCountersWithContext(ctx, true); e == nil {
		for _, n := range n {
			if re.MatchString(n.Name) && len(v.Networks) < 128 {
				v.Networks = append(v.Networks, agentwire.NetworkMetric{Name: n.Name, Sent: n.BytesSent, Received: n.BytesRecv})
			}
		}
	} else {
		v.Errors = append(v.Errors, "network unavailable")
	}
	return v
}
