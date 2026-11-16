package resourcebudget

import "strconv"

type DatabaseBudget struct {
	PoolMax, PoolMin                                int32
	Connections, SharedMiB, WorkMiB, MaintenanceMiB int
}

func Database(name string) (DatabaseBudget, error) {
	if name == "" {
		return DatabaseBudget{PoolMax: 10, PoolMin: 1}, nil
	}
	if _, err := Lookup(name); err != nil {
		return DatabaseBudget{}, err
	}
	switch name {
	case "light":
		return DatabaseBudget{8, 1, 40, 128, 2, 32}, nil
	case "standard":
		return DatabaseBudget{12, 1, 60, 256, 4, 64}, nil
	default:
		return DatabaseBudget{20, 2, 100, 512, 4, 128}, nil
	}
}
func (b DatabaseBudget) Parameters() map[string]string {
	return map[string]string{"max_connections": strconv.Itoa(b.Connections), "shared_buffers": strconv.Itoa(b.SharedMiB) + "MB", "work_mem": strconv.Itoa(b.WorkMiB) + "MB", "maintenance_work_mem": strconv.Itoa(b.MaintenanceMiB) + "MB"}
}
