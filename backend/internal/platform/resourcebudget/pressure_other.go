//go:build !linux

package resourcebudget

func containerMemoryPressure() error { return nil }
