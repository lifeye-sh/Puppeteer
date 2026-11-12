package identity

import (
	"encoding/json"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"testing"
	"time"
)

type passkeyTestUser struct{}

func (passkeyTestUser) WebAuthnID() []byte          { return []byte("stable-opaque-user-id") }
func (passkeyTestUser) WebAuthnName() string        { return "tester" }
func (passkeyTestUser) WebAuthnDisplayName() string { return "Test user" }
func (passkeyTestUser) WebAuthnCredentials() []webauthn.Credential {
	return []webauthn.Credential{{ID: []byte("existing-key")}}
}

func TestPasskeyProtocolOptions(t *testing.T) {
	origins := []string{"https://auth.example.com"}
	p, e := NewPasskeyProtocol("example.com", origins)
	if e != nil {
		t.Fatal(e)
	}
	origins[0] = "https://evil.example.com"
	if _, _, e = p.BeginLogin(origins[0]); e == nil {
		t.Fatal("caller mutated allowlist")
	}
	if _, _, e = p.BeginRegistration(passkeyTestUser{}, "https://evil.example.com"); e == nil {
		t.Fatal("foreign origin")
	}
	creation, registration, e := p.BeginRegistration(passkeyTestUser{}, "https://auth.example.com")
	if e != nil {
		t.Fatal(e)
	}
	if creation.Response.AuthenticatorSelection.UserVerification != protocol.VerificationRequired || creation.Response.AuthenticatorSelection.ResidentKey != protocol.ResidentKeyRequirementRequired || len(creation.Response.CredentialExcludeList) != 1 {
		t.Fatal("unsafe registration options")
	}
	_, login, e := p.BeginLogin("https://auth.example.com")
	if e != nil {
		t.Fatal(e)
	}
	if registration.Challenge == login.Challenge {
		t.Fatal("reused challenge")
	}
	for _, session := range []*webauthn.SessionData{registration, login} {
		if session.Origin != "https://auth.example.com" || session.RelyingPartyID != "example.com" || session.UserVerification != protocol.VerificationRequired || time.Until(session.Expires) > 5*time.Minute || time.Until(session.Expires) < 4*time.Minute {
			t.Fatal("invalid session constraints")
		}
		encoded, e := json.Marshal(session)
		if e != nil {
			t.Fatal(e)
		}
		var restored webauthn.SessionData
		if e = json.Unmarshal(encoded, &restored); e != nil || restored.Challenge != session.Challenge || restored.Origin != session.Origin {
			t.Fatal("session roundtrip", e)
		}
	}
}

func TestPasskeySessionPolicy(t *testing.T) {
	p, e := NewPasskeyProtocol("example.com", []string{"https://auth.example.com"})
	if e != nil {
		t.Fatal(e)
	}
	_, session, e := p.BeginLogin("https://auth.example.com")
	if e != nil {
		t.Fatal(e)
	}
	if !p.validSession(*session) {
		t.Fatal("fresh session rejected")
	}
	for _, mutate := range []func(*webauthn.SessionData){
		func(s *webauthn.SessionData) { s.Origin = "https://evil.example.com" },
		func(s *webauthn.SessionData) { s.RelyingPartyID = "evil.com" },
		func(s *webauthn.SessionData) { s.Expires = time.Now().Add(-time.Second) },
		func(s *webauthn.SessionData) { s.UserVerification = protocol.VerificationPreferred },
		func(s *webauthn.SessionData) { s.Challenge = "" },
	} {
		s := *session
		mutate(&s)
		if p.validSession(s) {
			t.Fatal("invalid session accepted")
		}
	}
	if _, e = p.FinishRegistration(passkeyTestUser{}, *session, nil); e == nil {
		t.Fatal("empty registration accepted")
	}
	if _, e = p.FinishLogin(nil, *session, nil); e == nil {
		t.Fatal("empty assertion accepted")
	}
}
