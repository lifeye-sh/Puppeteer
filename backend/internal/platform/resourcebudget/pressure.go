package resourcebudget

import (
	"context"
	"errors"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

// Pressure gates new derivative work only. It is a snapshot, not a reservation
// or an OOM guarantee; running sessions and retention are never stopped here.
func ExportPressure(ctx context.Context, directory string) error {
	usage, err := disk.UsageWithContext(ctx, directory)
	if err != nil {
		return errors.New("resource_probe_unavailable")
	}
	if usage.Free < 512<<20 {
		return errors.New("export_disk_pressure")
	}
	memory, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return errors.New("resource_probe_unavailable")
	}
	if memory.Available < 256<<20 || memory.Available < memory.Total/20 {
		return errors.New("export_memory_pressure")
	}
	return containerMemoryPressure()
}
