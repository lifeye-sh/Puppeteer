// Offline release tooling. Private signing keys never enter release artifacts.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"puppeteer/backend/internal/platform/systemupdate"
	"time"
)

func write(path string, v any, exclusive bool) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	b = append(b, '\n')
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if exclusive {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	f, e := os.OpenFile(path, flags, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return e
}
func keyID(pub ed25519.PublicKey) string { s := sha256.Sum256(pub); return hex.EncodeToString(s[:16]) }
func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: puppeteer-release-index init|sign [flags]")
	}
	f := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	keyPath := f.String("key", "", "private key file (never publish)")
	public := f.String("public", "", "public trust file for init")
	input := f.String("input", "", "unsigned catalog JSON for sign")
	output := f.String("output", "", "signed index JSON for sign")
	if e := f.Parse(os.Args[2:]); e != nil {
		return e
	}
	if *keyPath == "" || f.NArg() != 0 {
		return errors.New("key path required")
	}
	if os.Args[1] == "init" {
		if *public == "" || *public == *keyPath {
			return errors.New("separate public path required")
		}
		if _, e := os.Stat(*public); !os.IsNotExist(e) {
			return errors.New("public destination already exists or is inaccessible")
		}
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		if e = write(*keyPath, map[string]string{"privateKey": base64.StdEncoding.EncodeToString(key)}, true); e != nil {
			return e
		}
		if e = write(*public, map[string]string{keyID(pub): base64.StdEncoding.EncodeToString(pub)}, true); e != nil {
			return e
		}
		fmt.Println("Release signing key created; only the public trust file may be committed.")
		return nil
	}
	if os.Args[1] != "sign" || *input == "" || *output == "" || *output == *keyPath || *output == *input {
		return errors.New("sign requires separate input/output paths")
	}
	b, e := os.ReadFile(*keyPath)
	if e != nil {
		return e
	}
	var k struct {
		PrivateKey string `json:"privateKey"`
	}
	if json.Unmarshal(b, &k) != nil {
		return errors.New("invalid private key file")
	}
	key, e := base64.StdEncoding.Strict().DecodeString(k.PrivateKey)
	if e != nil || len(key) != ed25519.PrivateKeySize {
		return errors.New("invalid private key")
	}
	defer clear(key)
	b, e = os.ReadFile(*input)
	if e != nil {
		return e
	}
	var c systemupdate.Catalog
	if json.Unmarshal(b, &c) != nil {
		return errors.New("invalid catalog")
	}
	if e = systemupdate.Validate(c, time.Now()); e != nil {
		return e
	}
	payload, e := json.Marshal(c)
	if e != nil {
		return e
	}
	pub := ed25519.PrivateKey(key).Public().(ed25519.PublicKey)
	envelope := systemupdate.Envelope{KeyID: keyID(pub), Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))}
	if e = write(*output, envelope, false); e != nil {
		return e
	}
	fmt.Printf("Signed catalog sequence %d with %d releases\n", c.Sequence, len(c.Releases))
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
