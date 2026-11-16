// Package resourcebudget defines opt-in budgets, not measured session capacity.
package resourcebudget

import "errors"

type Profile struct {
	Name                                                     string
	HostMiB, CPUs                                            int
	ServerMiB, DatabaseMiB, GraphicsMiB, RendererMiB, WebMiB int
	GoMiB, HeavyTasks, ScratchMiB                            int
}

func Lookup(name string) (Profile, error) {
	switch name {
	case "light":
		return Profile{name, 4096, 2, 768, 512, 512, 1024, 128, 512, 1, 2048}, nil
	case "standard":
		return Profile{name, 8192, 4, 1536, 1024, 1024, 1536, 256, 1024, 1, 4096}, nil
	case "performance":
		return Profile{name, 16384, 8, 2048, 2048, 2048, 2048, 256, 1536, 2, 8192}, nil
	}
	return Profile{}, errors.New("资源档位必须为 light、standard 或 performance")
}

// Unset preserves one export plus one backup worker.
func HeavyLimit(name string) int {
	if name == "" {
		return 2
	}
	p, err := Lookup(name)
	if err != nil {
		panic(err)
	} // validated at startup
	return p.HeavyTasks
}
