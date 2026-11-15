//go:build linux

package main

import (
	"puppeteer/backend/internal/platform/rdpstorage"
	"os"
)

func main() { rdpstorage.Run(os.Args[1:]) }
