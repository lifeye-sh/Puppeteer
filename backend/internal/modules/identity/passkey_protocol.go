package identity

import (
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"next-terminal/backend/internal/platform/fault"
	"slices"
	"time"
)

// PasskeyProtocol is the protocol boundary; callers must persist SessionData
// verbatim and consume the server-side challenge atomically on completion.
type PasskeyProtocol struct {
	engine  *webauthn.WebAuthn
	origins []string
}

func (p *PasskeyProtocol) SessionAllowed(v webauthn.SessionData) bool { return p.validSession(v) }

func (p *PasskeyProtocol) RPID() string { return p.engine.Config.RPID }

func (p *PasskeyProtocol) validSession(session webauthn.SessionData) bool {
	return session.RelyingPartyID == p.engine.Config.RPID && slices.Contains(p.origins, session.Origin) && session.UserVerification == protocol.VerificationRequired && session.Challenge != "" && !session.Expires.IsZero() && time.Now().Before(session.Expires)
}

// FinishRegistration verifies protocol data only. The application must bind and
// consume the persisted ceremony before calling, and store the returned key.
func (p *PasskeyProtocol) FinishRegistration(user webauthn.User, session webauthn.SessionData, response *protocol.ParsedCredentialCreationData) (*webauthn.Credential, error) {
	if !p.validSession(session) || response == nil || user == nil {
		return nil, fault.Denied
	}
	return p.engine.CreateCredential(user, session, response)
}

// FinishLogin requires an owner lookup which checks the current account and
// credential revocation state. A successful result alone does not issue a cookie.
func (p *PasskeyProtocol) FinishLogin(handler webauthn.DiscoverableUserHandler, session webauthn.SessionData, response *protocol.ParsedCredentialAssertionData) (*webauthn.Credential, error) {
	if !p.validSession(session) || response == nil || handler == nil {
		return nil, fault.Denied
	}
	return p.engine.ValidateDiscoverableLogin(handler, session, response)
}

func NewPasskeyProtocol(rp string, origins []string) (*PasskeyProtocol, error) {
	if e := ValidatePasskeyConfig(rp, origins); e != nil {
		return nil, e
	}
	engine, e := webauthn.New(&webauthn.Config{
		RPID: rp, RPDisplayName: "Puppeteer", RPOrigins: slices.Clone(origins),
		AttestationPreference:  protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired},
	})
	if e != nil {
		return nil, e
	}
	return &PasskeyProtocol{engine: engine, origins: slices.Clone(origins)}, nil
}

func (p *PasskeyProtocol) BeginRegistration(user webauthn.User, origin string) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	if !slices.Contains(p.origins, origin) {
		return nil, nil, fault.Denied
	}
	exclusions := []protocol.CredentialDescriptor{}
	for _, c := range user.WebAuthnCredentials() {
		exclusions = append(exclusions, c.Descriptor())
	}
	options, session, e := p.engine.BeginRegistration(user, webauthn.WithRegistrationOrigin(origin), webauthn.WithExclusions(exclusions))
	if e == nil {
		session.Expires = time.Now().Add(5 * time.Minute)
	}
	return options, session, e
}

func (p *PasskeyProtocol) BeginLogin(origin string) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	if !slices.Contains(p.origins, origin) {
		return nil, nil, fault.Denied
	}
	options, session, e := p.engine.BeginDiscoverableLogin(webauthn.WithLoginOrigin(origin), webauthn.WithUserVerification(protocol.VerificationRequired))
	if e == nil {
		session.Expires = time.Now().Add(5 * time.Minute)
	}
	return options, session, e
}
