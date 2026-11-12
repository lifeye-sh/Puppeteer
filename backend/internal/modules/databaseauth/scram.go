package databaseauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const SCRAMIterations = 32768

// SCRAMMaterial contains server verification keys, never a recoverable password.
// The enclosing bundle must be encrypted by Vault before persistence.
type SCRAMMaterial struct {
	Salt       []byte `json:"salt"`
	Iterations int    `json:"iterations"`
	StoredKey  []byte `json:"storedKey"`
	ServerKey  []byte `json:"serverKey"`
}
type passwordBundle struct {
	Version int           `json:"version"`
	MySQL   string        `json:"mysql"`
	SCRAM   SCRAMMaterial `json:"scram"`
}

func mac(key []byte, label string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(label))
	return h.Sum(nil)
}
func bundleVerifier(password, mysql string) (string, error) {
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	// Passwords are generated base64url ASCII, so SASLprep is an identity here.
	salted := pbkdf2.Key([]byte(password), salt, SCRAMIterations, 32, sha256.New)
	defer clear(salted)
	clientKey := mac(salted, "Client Key")
	defer clear(clientKey)
	stored := sha256.Sum256(clientKey)
	v := passwordBundle{Version: 2, MySQL: mysql, SCRAM: SCRAMMaterial{Salt: salt, Iterations: SCRAMIterations, StoredKey: stored[:], ServerKey: mac(salted, "Server Key")}}
	defer clear(v.SCRAM.ServerKey)
	raw, e := json.Marshal(v)
	defer clear(raw)
	return string(raw), e
}
func parseBundle(encoded string) (passwordBundle, error) {
	var v passwordBundle
	if len(encoded) > 1024 || json.Unmarshal([]byte(encoded), &v) != nil || v.Version != 2 ||
		!strings.HasPrefix(v.MySQL, prefix) || len(v.MySQL) > 160 || v.SCRAM.Iterations != SCRAMIterations ||
		len(v.SCRAM.Salt) != 16 || len(v.SCRAM.StoredKey) != 32 || len(v.SCRAM.ServerKey) != 32 {
		return passwordBundle{}, ErrInvalid
	}
	return v, nil
}

// SCRAMCredentials deliberately rejects legacy Argon2-only records. Those users
// must rotate their personal database password; it cannot be derived backwards.
func SCRAMCredentials(encoded string) (SCRAMMaterial, error) {
	v, e := parseBundle(encoded)
	return v.SCRAM, e
}
