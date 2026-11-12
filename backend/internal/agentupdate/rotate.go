package agentupdate

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func rootChain(repo string) ([]string, *metadata.Metadata[metadata.RootType], error) {
	paths, e := filepath.Glob(filepath.Join(repo, "*.root.json"))
	if e != nil {
		return nil, nil, e
	}
	if len(paths) == 0 || len(paths) > 1024 {
		return nil, nil, errors.New("invalid root chain length")
	}
	sort.Slice(paths, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.Split(filepath.Base(paths[i]), ".")[0])
		b, _ := strconv.Atoi(strings.Split(filepath.Base(paths[j]), ".")[0])
		return a < b
	})
	var previous *metadata.Metadata[metadata.RootType]
	names := []string{}
	for i, path := range paths {
		name := fmt.Sprintf("%d.root.json", i+1)
		info, e := os.Lstat(path)
		if e != nil {
			return nil, nil, e
		}
		if filepath.Base(path) != name || !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return nil, nil, errors.New("invalid or noncontiguous root chain")
		}
		root, e := metadata.Root().FromFile(path)
		if e != nil {
			return nil, nil, e
		}
		if root.Signed.Version != int64(i+1) {
			return nil, nil, errors.New("root version mismatch")
		}
		if previous != nil {
			if e = previous.VerifyDelegate("root", root); e != nil {
				return nil, nil, e
			}
		}
		if e = root.VerifyDelegate("root", root); e != nil {
			return nil, nil, e
		}
		previous = root
		names = append(names, name)
	}
	if !previous.Signed.Expires.After(time.Now()) {
		return nil, nil, errors.New("latest root expired")
	}
	return names, previous, nil
}

// RotateRoot prepares new public and private directories; active files are untouched.
func RotateRoot(repo, keys, output, newKeys string) (err error) {
	dirs := []string{repo, keys, output, newKeys}
	for i := range dirs {
		for j := 0; j < i; j++ {
			if e := separateDirectories(dirs[i], dirs[j]); e != nil {
				return e
			}
		}
	}
	_, old, e := rootChain(repo)
	if e != nil {
		return e
	}
	oldKey, e := readSigningKey(filepath.Join(keys, "root.key"))
	if e != nil {
		return e
	}
	oldPub, e := metadata.KeyFromPublicKey(oldKey.Public())
	if e != nil {
		return e
	}
	role := old.Signed.Roles["root"]
	keyID, e := oldPub.ID()
	if e != nil {
		return e
	}
	if role == nil || role.Threshold != 1 || len(role.KeyIDs) != 1 || role.KeyIDs[0] != keyID {
		return errors.New("rotation requires the current single root signing key")
	}
	if _, e = os.Stat(output); !os.IsNotExist(e) {
		return errors.New("new public directory already exists or inaccessible")
	}
	if e = os.Mkdir(newKeys, 0700); e != nil {
		return e
	}
	createdOutput := false
	defer func() {
		if err != nil {
			os.RemoveAll(newKeys)
			if createdOutput {
				os.RemoveAll(output)
			}
		}
	}()
	pub, nextKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		return e
	}
	for _, name := range []string{"root", "targets", "snapshot", "timestamp"} {
		key := nextKey
		if name != "root" {
			key, e = readSigningKey(filepath.Join(keys, name+".key"))
			if e != nil {
				return e
			}
		}
		if e = os.WriteFile(filepath.Join(newKeys, name+".key"), key, 0600); e != nil {
			return e
		}
	}
	raw, e := old.ToBytes(false)
	if e != nil {
		return e
	}
	next, e := metadata.Root().FromBytes(raw)
	if e != nil {
		return e
	}
	if e = next.Signed.RevokeKey(role.KeyIDs[0], "root"); e != nil {
		return e
	}
	k, e := metadata.KeyFromPublicKey(pub)
	if e != nil {
		return e
	}
	if e = next.Signed.AddKey(k, "root"); e != nil {
		return e
	}
	next.Signed.Version++
	next.Signed.Expires = time.Now().Add(365 * 24 * time.Hour)
	next.Signatures = nil
	for _, key := range []ed25519.PrivateKey{oldKey, nextKey} {
		s, e := signature.LoadSigner(key, crypto.Hash(0))
		if e != nil {
			return e
		}
		if _, e = next.Sign(s); e != nil {
			return e
		}
	}
	if e = old.VerifyDelegate("root", next); e != nil {
		return e
	}
	if e = next.VerifyDelegate("root", next); e != nil {
		return e
	}
	if e = ExportRepository(repo, output); e != nil {
		return e
	}
	createdOutput = true
	return next.ToFile(filepath.Join(output, fmt.Sprintf("%d.root.json", next.Signed.Version)), true)
}
