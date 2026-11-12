package operations

import (
	"context"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"runtime"
	"time"
)

type PoolMetric struct {
	Total               int32 `json:"total"`
	Acquired            int32 `json:"acquired"`
	Idle                int32 `json:"idle"`
	Max                 int32 `json:"max"`
	AcquireCount        int64 `json:"acquireCount"`
	Cancelled           int64 `json:"cancelled"`
	EmptyWaitCount      int64 `json:"emptyWaitCount"`
	AcquireMilliseconds int64 `json:"acquireMilliseconds"`
}
type HostMetric struct {
	CPU         *float64 `json:"cpu"`
	MemoryTotal *uint64  `json:"memoryTotal"`
	MemoryUsed  *uint64  `json:"memoryUsed"`
}
type BusinessMetric struct {
	Sessions       int64 `json:"sessions"`
	Reserved       int64 `json:"reserved"`
	Connected      int64 `json:"connected"`
	StorageSpaces  int64 `json:"storageSpaces"`
	StorageUsed    int64 `json:"storageUsed"`
	StorageQuota   int64 `json:"storageQuota"`
	RecordingBytes int64 `json:"recordingBytes"`
	DatabaseBytes  int64 `json:"databaseBytes"`
}
type Overview struct {
	SampledAt    time.Time       `json:"sampledAt"`
	Node         string          `json:"node"`
	Pool         PoolMetric      `json:"pool"`
	HeapBytes    uint64          `json:"heapBytes"`
	RuntimeBytes uint64          `json:"runtimeBytes"`
	Goroutines   int             `json:"goroutines"`
	Host         HostMetric      `json:"host"`
	Business     *BusinessMetric `json:"business"`
	Errors       []string        `json:"errors"`
}

func (s *Service) Overview(ctx context.Context, node string) Overview {
	v := Overview{SampledAt: time.Now().UTC(), Node: node, Errors: []string{}}
	stats := s.db.PoolStats()
	v.Pool = PoolMetric{stats.TotalConns(), stats.AcquiredConns(), stats.IdleConns(), stats.MaxConns(), stats.AcquireCount(), stats.CanceledAcquireCount(), stats.EmptyAcquireCount(), stats.AcquireDuration().Milliseconds()}
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	v.HeapBytes = memory.HeapAlloc
	v.RuntimeBytes = memory.Sys
	v.Goroutines = runtime.NumGoroutine()
	if samples, err := cpu.PercentWithContext(ctx, 100*time.Millisecond, false); err == nil && len(samples) == 1 {
		v.Host.CPU = &samples[0]
	} else {
		v.Errors = append(v.Errors, "host_cpu_unavailable")
	}
	if m, err := mem.VirtualMemoryWithContext(ctx); err == nil {
		v.Host.MemoryTotal = &m.Total
		v.Host.MemoryUsed = &m.Used
	} else {
		v.Errors = append(v.Errors, "host_memory_unavailable")
	}
	// Logical storage ledger, not bytea scans. Physical DB size includes its indexes.
	var b BusinessMetric
	if err := s.db.Row(ctx, `SELECT
 (SELECT count(*) FROM sessions WHERE closed_at IS NULL AND lease_expires_at>now()),
 (SELECT count(*) FROM sessions WHERE state='reserved' AND closed_at IS NULL AND lease_expires_at>now()),
 (SELECT count(*) FROM sessions WHERE state='connected' AND closed_at IS NULL AND lease_expires_at>now()),
 (SELECT count(*) FROM user_storage),(SELECT coalesce(sum(used_bytes),0)::bigint FROM user_storage),
 (SELECT coalesce(sum(quota_bytes),0)::bigint FROM user_storage),
 (SELECT coalesce(sum(bytes),0)::bigint FROM recordings),pg_database_size(current_database())`).Scan(&b.Sessions, &b.Reserved, &b.Connected, &b.StorageSpaces, &b.StorageUsed, &b.StorageQuota, &b.RecordingBytes, &b.DatabaseBytes); err == nil {
		v.Business = &b
	} else {
		v.Errors = append(v.Errors, "business_metrics_unavailable")
	}
	return v
}
