package agentupdate

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"os"
	"path/filepath"
	"time"
)

// InitRepository is an explicit offline publishing operation, never a server endpoint.
func InitRepository(repo, keys string) error {
	if e := separateDirectories(repo, keys); e != nil {
		return e
	}
	if _, e := os.Stat(filepath.Join(repo, "1.root.json")); !os.IsNotExist(e) {
		return errors.New("repository already initialized or inaccessible")
	}
	if e := os.MkdirAll(repo, 0700); e != nil {
		return e
	}
	if e := os.MkdirAll(keys, 0700); e != nil {
		return e
	}
	root := metadata.Root(time.Now().Add(365 * 24 * time.Hour))
	root.Signed.ConsistentSnapshot = false
	for _, role := range []string{"root", "targets", "snapshot", "timestamp"} {
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		f, e := os.OpenFile(filepath.Join(keys, role+".key"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.Write(key)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		k, e := metadata.KeyFromPublicKey(pub)
		if e != nil {
			return e
		}
		if e = root.Signed.AddKey(k, role); e != nil {
			return e
		}
		if role == "root" {
			signer, e := signature.LoadSigner(key, crypto.Hash(0))
			if e != nil {
				return e
			}
			_ = signer
		}
	}
	key, e := readSigningKey(filepath.Join(keys, "root.key"))
	if e != nil {
		return e
	}
	signer, e := signature.LoadSigner(key, crypto.Hash(0))
	if e != nil {
		return e
	}
	if _, e = root.Sign(signer); e != nil {
		return e
	}
	return root.ToFile(filepath.Join(repo, "1.root.json"), true)
}
func readSigningKey(path string) (ed25519.PrivateKey, error) {
	info, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != ed25519.PrivateKeySize {
		return nil, errors.New("signing key must be private Ed25519 file")
	}
	raw, e := os.ReadFile(path)
	return ed25519.PrivateKey(raw), e
}
func Publish(repo, keys, binary string, release Release, expires time.Time) error {
	if e := separateDirectories(repo, keys); e != nil {
		return e
	}
	if release.Sequence <= 0 || release.Version == "" || release.OS != "linux" || release.Arch != "amd64" {
		return errors.New("invalid release")
	}
	if previous, e := metadata.Timestamp().FromFile(filepath.Join(repo, "timestamp.json")); e == nil {
		if previous.Signed.Version >= release.Sequence {
			return errors.New("release sequence must increase")
		}
	} else if !os.IsNotExist(e) {
		return fmt.Errorf("read previous publication: %w", e)
	}
	targets := metadata.Targets(expires)
	targets.Signed.Version = release.Sequence
	target, e := metadata.TargetFile().FromFile(binary, "sha256")
	if e != nil {
		return e
	}
	if target.Length <= 0 || target.Length > 128*1024*1024 {
		return errors.New("invalid release length")
	}
	custom, _ := json.Marshal(release)
	raw := json.RawMessage(custom)
	target.Custom = &raw
	name := "nt-agent-" + release.OS + "-" + release.Arch
	targets.Signed.Targets[name] = target
	snapshot := metadata.Snapshot(expires)
	snapshot.Signed.Version = release.Sequence
	snapshot.Signed.Meta["targets.json"] = metadata.MetaFile(release.Sequence)
	timestamp := metadata.Timestamp(expires)
	timestamp.Signed.Version = release.Sequence
	timestamp.Signed.Meta["snapshot.json"] = metadata.MetaFile(release.Sequence)
	for _, role := range []string{"targets", "snapshot", "timestamp"} {
		key, e := readSigningKey(filepath.Join(keys, role+".key"))
		if e != nil {
			return e
		}
		signer, e := signature.LoadSigner(key, crypto.Hash(0))
		if e != nil {
			return e
		}
		switch role {
		case "targets":
			_, e = targets.Sign(signer)
		case "snapshot":
			_, e = snapshot.Sign(signer)
		case "timestamp":
			_, e = timestamp.Sign(signer)
		}
		if e != nil {
			return e
		}
	}
	if e = os.MkdirAll(filepath.Join(repo, "targets"), 0700); e != nil {
		return e
	}
	data, e := os.ReadFile(binary)
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(repo, "targets", name), data, 0600); e != nil {
		return e
	}
	// Timestamp is the publication barrier; partial publications fail verification.
	for _, role := range []string{"targets", "snapshot", "timestamp"} {
		path := filepath.Join(repo, role+".json")
		tmp := path + fmt.Sprintf(".%d.tmp", release.Sequence)
		switch role {
		case "targets":
			e = targets.ToFile(tmp, true)
		case "snapshot":
			e = snapshot.ToFile(tmp, true)
		case "timestamp":
			e = timestamp.ToFile(tmp, true)
		}
		if e != nil {
			return e
		}
		if e = os.Rename(tmp, path); e != nil {
			return e
		}
	}
	return nil
}
