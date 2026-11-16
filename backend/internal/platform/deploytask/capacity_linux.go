//go:build linux

package deploytask

import (
	"errors"
	"golang.org/x/sys/unix"
	"path/filepath"
)

func checkResourceDisk(path string) error {
	var fs unix.Statfs_t
	if !filepath.IsAbs(path) || unix.Statfs(path, &fs) != nil {
		return errors.New("无法读取 Docker 数据盘可用空间；执行器必须部署在 Docker 宿主机")
	}
	if uint64(fs.Bavail)*uint64(fs.Bsize) < 1280<<20 {
		return errors.New("Docker 数据盘空间不足，需要至少 1280 MiB 可用空间用于转换及余量")
	}
	return nil
}
