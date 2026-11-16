//go:build !linux

package deploytask

import "errors"

func checkResourceDisk(string) error {
	return errors.New("资源档位需要 Linux 宿主机部署执行器")
}
