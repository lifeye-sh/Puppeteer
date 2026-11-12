package agentwire

import "time"

type DiskMetric struct {
	Path  string `json:"path"`
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
}
type NetworkMetric struct {
	Name     string `json:"name"`
	Sent     uint64 `json:"sent"`
	Received uint64 `json:"received"`
}

// Nil values mean unavailable, never a fabricated zero.
type Metrics struct {
	IntervalSeconds int             `json:"intervalSeconds"`
	Update          *UpdateStatus   `json:"update,omitempty"`
	CollectedAt     time.Time       `json:"collectedAt"`
	CPU             *float64        `json:"cpu"`
	MemoryTotal     uint64          `json:"memoryTotal"`
	MemoryUsed      uint64          `json:"memoryUsed"`
	Disks           []DiskMetric    `json:"disks"`
	Networks        []NetworkMetric `json:"networks"`
	Errors          []string        `json:"errors"`
}

type UpdateStatus struct {
	State     string    `json:"state"`
	Version   string    `json:"version"`
	Sequence  int64     `json:"sequence"`
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}
