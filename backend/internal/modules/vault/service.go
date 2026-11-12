package vault

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"golang.org/x/crypto/ssh"
	"next-terminal/backend/internal/platform/fault"
	"next-terminal/backend/internal/platform/postgres"
	"next-terminal/backend/internal/platform/secretbox"
	"strings"
	"unicode/utf8"
)

type Service struct {
	db  *postgres.DB
	key []byte
}

func New(db *postgres.DB, key []byte) *Service { return &Service{db, key} }

type Credential struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Username      string `json:"username"`
	Version       int64  `json:"version"`
	ActiveVersion int64  `json:"activeVersion"`
	PublicKey     string `json:"publicKey"`
	AssetCount    int    `json:"assetCount"`
}
type Secret struct {
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"privateKey,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}
type Write struct {
	Credential
	Secret   *Secret `json:"secret,omitempty"`
	Generate bool    `json:"generate,omitempty"`
}

func (s *Service) List(ctx context.Context) ([]Credential, error) {
	rows, e := s.db.Rows(ctx, `SELECT id,name,kind,username,version,active_version,public_key FROM credentials WHERE deleted_at IS NULL ORDER BY name,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Credential{}
	for rows.Next() {
		var v Credential
		if e = rows.Scan(&v.ID, &v.Name, &v.Kind, &v.Username, &v.Version, &v.ActiveVersion, &v.PublicKey); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) Get(ctx context.Context, id string) (v Credential, e error) {
	e = s.db.Row(ctx, `SELECT id,name,kind,username,version,active_version,public_key FROM credentials WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&v.ID, &v.Name, &v.Kind, &v.Username, &v.Version, &v.ActiveVersion, &v.PublicKey)
	return
}
func Validate(in *Write) error {
	if strings.TrimSpace(in.Name) == "" || utf8.RuneCountInString(in.Name) > 100 || strings.TrimSpace(in.Username) == "" || len(in.Username) > 128 || (in.Kind != "password" && in.Kind != "private-key") {
		return fault.Invalid
	}
	if in.Generate {
		if in.Kind != "private-key" || in.Secret != nil {
			return fault.Invalid
		}
		_, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		block, e := ssh.MarshalPrivateKey(key, "next-terminal")
		if e != nil {
			return e
		}
		in.Secret = &Secret{PrivateKey: string(pem.EncodeToMemory(block))}
	}
	if in.Secret == nil {
		if in.ID == "" {
			return fault.Invalid
		}
		return nil
	}
	x := in.Secret
	if len(x.Password) > 4096 || len(x.PrivateKey) > 32768 || len(x.Passphrase) > 4096 {
		return fault.Invalid
	}
	if in.Kind == "password" {
		if x.Password == "" || x.PrivateKey != "" || x.Passphrase != "" {
			return fault.Invalid
		}
		in.PublicKey = ""
		return nil
	}
	if x.Password != "" || x.PrivateKey == "" {
		return fault.Invalid
	}
	var signer ssh.Signer
	var e error
	if x.Passphrase != "" {
		signer, e = ssh.ParsePrivateKeyWithPassphrase([]byte(x.PrivateKey), []byte(x.Passphrase))
	} else {
		signer, e = ssh.ParsePrivateKey([]byte(x.PrivateKey))
	}
	if e != nil {
		return fault.Invalid
	}
	in.PublicKey = string(ssh.MarshalAuthorizedKey(signer.PublicKey()))
	return nil
}

// Caller owns the transaction and reference coordination. Stored versions are immutable.
func (s *Service) Save(ctx context.Context, in Write) (string, error) {
	if e := Validate(&in); e != nil {
		return "", e
	}
	if in.ID == "" {
		if e := s.db.Row(ctx, `INSERT INTO credentials(name,kind,username) VALUES($1,$2,$3) RETURNING id`, in.Name, in.Kind, in.Username).Scan(&in.ID); e != nil {
			return "", e
		}
		in.Version = 1
	} else {
		old, e := s.Get(ctx, in.ID)
		if e != nil {
			return "", e
		}
		if old.Version != in.Version || old.Kind != in.Kind {
			return "", fault.Conflict
		}
		tag, e := s.db.Exec(ctx, `UPDATE credentials SET name=$2,username=$3,version=version+1 WHERE id=$1 AND version=$4 AND deleted_at IS NULL`, in.ID, in.Name, in.Username, in.Version)
		if e != nil {
			return "", e
		}
		if tag.RowsAffected() != 1 {
			return "", fault.Conflict
		}
		in.Version++
	}
	if in.Secret != nil {
		raw, e := json.Marshal(in.Secret)
		if e != nil {
			return "", e
		}
		defer clear(raw)
		aad := fmt.Sprintf("credential:%s:%d", in.ID, in.Version)
		sealed, wrapped, e := secretbox.Wrap(s.key, raw, aad)
		if e != nil {
			return "", e
		}
		if _, e = s.db.Exec(ctx, `INSERT INTO secret_versions(credential_id,version,ciphertext,wrapped_dek) VALUES($1,$2,$3,$4)`, in.ID, in.Version, sealed, wrapped); e != nil {
			return "", e
		}
		if _, e = s.db.Exec(ctx, `UPDATE credentials SET active_version=$2,public_key=$3 WHERE id=$1`, in.ID, in.Version, in.PublicKey); e != nil {
			return "", e
		}
	}
	return in.ID, nil
}
func (s *Service) Delete(ctx context.Context, id string) error {
	tag, e := s.db.Exec(ctx, `UPDATE credentials SET deleted_at=now(),version=version+1 WHERE id=$1 AND deleted_at IS NULL`, id)
	if e == nil && tag.RowsAffected() != 1 {
		return fault.Conflict
	}
	return e
}

// Reveal is only called after application-level password + mandatory TOTP reauthentication.
func (s *Service) Reveal(ctx context.Context, id, actor, requestID string) (Secret, error) {
	return s.readSecret(ctx, id, actor, requestID, "admin_reveal")
}

// SSHAuth is an internal port called only after a current resource grant and session lease check.
// It returns an authentication method to the transport, never a secret in an HTTP response.
func (s *Service) SSHAuth(ctx context.Context, id, actor, sessionID string) (Credential, ssh.AuthMethod, error) {
	return s.sshAuth(ctx, id, actor, sessionID, "ssh_session")
}

// InstallationAuth is called after administrator, asset and host-trust checks.
func (s *Service) InstallationAuth(ctx context.Context, id, actor, jobID string) (Credential, ssh.AuthMethod, error) {
	return s.sshAuth(ctx, id, actor, jobID, "agent_installation")
}

func (s *Service) TaskAuth(ctx context.Context, id, actor, jobID string) (Credential, ssh.AuthMethod, error) {
	return s.sshAuth(ctx, id, actor, jobID, "scheduled_task")
}

func (s *Service) sshAuth(ctx context.Context, id, actor, sessionID, purpose string) (Credential, ssh.AuthMethod, error) {
	// Application calls this within a transaction: keep metadata and encrypted version consistent.
	var locked string
	if e := s.db.Row(ctx, `SELECT id FROM credentials WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&locked); e != nil {
		return Credential{}, nil, e
	}
	v, e := s.Get(ctx, id)
	if e != nil {
		return v, nil, e
	}
	x, e := s.readSecret(ctx, id, actor, sessionID, purpose)
	if e != nil {
		return v, nil, e
	}
	if v.Kind == "password" {
		return v, ssh.Password(x.Password), nil
	}
	var signer ssh.Signer
	if x.Passphrase != "" {
		signer, e = ssh.ParsePrivateKeyWithPassphrase([]byte(x.PrivateKey), []byte(x.Passphrase))
	} else {
		signer, e = ssh.ParsePrivateKey([]byte(x.PrivateKey))
	}
	if e != nil {
		return v, nil, e
	}
	return v, ssh.PublicKeys(signer), nil
}
func (s *Service) readSecret(ctx context.Context, id, actor, requestID, purpose string) (Secret, error) {
	v, e := s.Get(ctx, id)
	if e != nil {
		return Secret{}, e
	}
	var sealed, wrapped []byte
	if e = s.db.Row(ctx, `SELECT ciphertext,wrapped_dek FROM secret_versions WHERE credential_id=$1 AND version=$2`, id, v.ActiveVersion).Scan(&sealed, &wrapped); e != nil {
		return Secret{}, e
	}
	raw, e := secretbox.Unwrap(s.key, sealed, wrapped, fmt.Sprintf("credential:%s:%d", id, v.ActiveVersion))
	if e != nil {
		return Secret{}, e
	}
	defer clear(raw)
	var out Secret
	if e = json.Unmarshal(raw, &out); e != nil {
		return Secret{}, e
	}
	_, e = s.db.Exec(ctx, `INSERT INTO secret_access_events(actor_id,credential_id,version,purpose,request_id) VALUES($1,$2,$3,$4,$5)`, actor, id, v.ActiveVersion, purpose, requestID)
	return out, e
}

// ProxyAuth is an internal, audited credential port; never an HTTP secret response.
func (s *Service) ProxyAuth(ctx context.Context, id, actor, request string) (Credential, string, error) {
	return s.passwordAuth(ctx, id, actor, request, "network_proxy")
}
func (s *Service) GraphicsAuth(ctx context.Context, id, actor, request string) (Credential, string, error) {
	return s.passwordAuth(ctx, id, actor, request, "graphics_session")
}
func (s *Service) passwordAuth(ctx context.Context, id, actor, request, purpose string) (Credential, string, error) {
	var locked string
	if e := s.db.Row(ctx, `SELECT id FROM credentials WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, id).Scan(&locked); e != nil {
		return Credential{}, "", e
	}
	c, e := s.Get(ctx, id)
	if e != nil {
		return c, "", e
	}
	if c.Kind != "password" {
		return c, "", fault.Invalid
	}
	secret, e := s.readSecret(ctx, id, actor, request, purpose)
	return c, secret.Password, e
}
