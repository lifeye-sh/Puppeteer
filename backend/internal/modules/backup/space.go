package backup

import (
	"context"
	"errors"
	"github.com/shirou/gopsutil/v4/disk"
)

func RequireSpace(ctx context.Context, path string, bytes uint64) error {
	usage, e := disk.UsageWithContext(ctx, path)
	if e != nil {
		return errors.New("backup disk space unavailable")
	}
	if usage.Free < bytes {
		return errors.New("insufficient backup disk space")
	}
	return nil
}
