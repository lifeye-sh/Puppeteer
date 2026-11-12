package main

import (
	"flag"
	"log"
	"next-terminal/backend/internal/agentupdate"
	"time"
)

func main() {
	repo := flag.String("repo", "", "public repository directory")
	keys := flag.String("keys", "", "private offline signing key directory")
	init := flag.Bool("init", false, "initialize trust root once; keep root.key offline")
	binary := flag.String("binary", "", "Linux amd64 Agent executable")
	version := flag.String("version", "", "release label")
	sequence := flag.Int64("sequence", 0, "strictly increasing release number")
	export := flag.String("export", "", "export validated public files to a new directory (no private keys)")
	rotate := flag.String("rotate-root", "", "prepare rotated public repository in a new directory")
	newKeys := flag.String("new-keys", "", "new private key directory for root rotation")
	flag.Parse()
	var e error
	if *rotate != "" {
		if *repo == "" || *keys == "" || *newKeys == "" || *export != "" || *init || *binary != "" {
			log.Fatal("rotation requires repo, keys, rotate-root and new-keys only")
		}
		if e = agentupdate.RotateRoot(*repo, *keys, *rotate, *newKeys); e != nil {
			log.Fatal(e)
		}
		return
	}
	if *newKeys != "" {
		log.Fatal("new-keys requires rotate-root")
	}
	if *export != "" {
		if *repo == "" || *init || *binary != "" {
			log.Fatal("export requires repo and cannot be combined with init or binary")
		}
		if e = agentupdate.ExportRepository(*repo, *export); e != nil {
			log.Fatal(e)
		}
		return
	}
	if *repo == "" || *keys == "" {
		log.Fatal("repo and keys required")
	}
	if *init {
		e = agentupdate.InitRepository(*repo, *keys)
	} else {
		e = agentupdate.Publish(*repo, *keys, *binary, agentupdate.Release{Sequence: *sequence, Version: *version, OS: "linux", Arch: "amd64"}, time.Now().Add(24*time.Hour))
	}
	if e != nil {
		log.Fatal(e)
	}
}
