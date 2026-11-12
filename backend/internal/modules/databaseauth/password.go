// Package databaseauth implements the verifier for TLS-only MySQL full authentication.
// Verifiers must be encrypted by the vault before persistence; they are not MySQL scrambles.
package databaseauth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"golang.org/x/crypto/argon2"
)

var ErrBusy = errors.New("database authentication capacity exhausted")
var ErrInvalid = errors.New("invalid database credential")

const prefix = "$argon2id$v=19$m=65536,t=3,p=1$"

// Verifier bounds both in-flight Argon2 memory and queued authentication work.
// Create and Verify share the same pool. Create's plaintext is returned once only.
type Verifier struct {
	active   chan struct{}
	admitted chan struct{}
}

func NewVerifier() *Verifier {
	return &Verifier{active: make(chan struct{}, 8), admitted: make(chan struct{}, 40)}
}
func (v *Verifier) acquire(ctx context.Context) (func(), error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	select {
	case v.admitted <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	select {
	case v.active <- struct{}{}:
		if e := ctx.Err(); e != nil {
			<-v.active
			<-v.admitted
			return nil, e
		}
		return func() { <-v.active; <-v.admitted }, nil
	case <-ctx.Done():
		<-v.admitted
		return nil, ctx.Err()
	}
}
func (v *Verifier) Create(ctx context.Context) (password, verifier string, err error) {
	release, err := v.acquire(ctx)
	if err != nil {
		return "", "", err
	}
	defer release()
	secret := make([]byte, 32)
	salt := make([]byte, 16)
	defer clear(secret)
	if _, err = rand.Read(secret); err != nil {
		return "", "", err
	}
	if _, err = rand.Read(salt); err != nil {
		return "", "", err
	}
	password = base64.RawURLEncoding.EncodeToString(secret)
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
	defer clear(hash)
	if err = ctx.Err(); err != nil {
		return "", "", err
	}
	verifier = prefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(hash)
	verifier, err = bundleVerifier(password, verifier)
	if err != nil {
		return "", "", err
	}
	if err = ctx.Err(); err != nil {
		return "", "", err
	}
	return password, verifier, nil
}
func (v *Verifier) Verify(ctx context.Context, password, encoded string) error {
	if strings.HasPrefix(encoded, "{") {
		bundle, e := parseBundle(encoded)
		if e != nil {
			return e
		}
		encoded = bundle.MySQL
	}
	// Only this fixed cost profile is accepted, so stored data cannot request
	// unbounded Argon2 memory/time. Database password length is also bounded.
	if len(password) != 43 || !strings.HasPrefix(encoded, prefix) || len(encoded) > 160 {
		return ErrInvalid
	}
	parts := strings.Split(strings.TrimPrefix(encoded, prefix), "$")
	if len(parts) != 2 {
		return ErrInvalid
	}
	salt, e := base64.RawStdEncoding.Strict().DecodeString(parts[0])
	if e != nil || len(salt) != 16 {
		return ErrInvalid
	}
	expected, e := base64.RawStdEncoding.Strict().DecodeString(parts[1])
	if e != nil || len(expected) != 32 {
		return ErrInvalid
	}
	defer clear(expected)
	release, e := v.acquire(ctx)
	if e != nil {
		return e
	}
	defer release()
	actual := argon2.IDKey([]byte(password), salt, 3, 64*1024, 1, 32)
	defer clear(actual)
	if e = ctx.Err(); e != nil {
		return e
	}
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return ErrInvalid
	}
	return nil
}

// SplitIdentity keeps the wire identity unambiguous: both platform username and
// asset alias must individually forbid '@'. Authorization follows verification.
func SplitIdentity(wire string) (username, alias string, err error) {
	if len(wire) > 256 || strings.Count(wire, "@") != 1 || strings.ContainsAny(wire, "\x00\r\n") {
		return "", "", ErrInvalid
	}
	username, alias, _ = strings.Cut(wire, "@")
	if username == "" || alias == "" || strings.TrimSpace(username) != username || strings.TrimSpace(alias) != alias {
		return "", "", ErrInvalid
	}
	return
}
