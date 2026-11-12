package identity

import (
	"context"
	"next-terminal/backend/internal/modules/system"
)

type OAuthLifetimes struct{ Access, Refresh, IDToken, Code int }

// Issuance callers hold a transaction. The shared head lock keeps all lifetime
// reads in a token exchange coherent with one configuration revision, including
// family creation, access issuance and ID-token signing. Existing token/family
// deadlines are immutable; a refresh never extends its family's lifetime.
func (s *Service) OAuthLifetimes(ctx context.Context, client string) (OAuthLifetimes, error) {
	var id int64
	if e := s.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR SHARE`).Scan(&id); e != nil {
		return OAuthLifetimes{}, e
	}
	v, e := system.New(s.db).Configuration(ctx, id)
	if e != nil {
		return OAuthLifetimes{}, e
	}
	if e = system.ValidateConfiguration(v.Values); e != nil {
		return OAuthLifetimes{}, e
	}
	out := OAuthLifetimes{Access: v.Values.Int("oidc-server-access-token-ttl"), Refresh: v.Values.Int("oidc-server-refresh-token-ttl"), IDToken: v.Values.Int("oidc-server-id-token-ttl"), Code: v.Values.Int("oidc-server-auth-code-ttl")}
	if client == NativeClientUUID {
		out.Access = min(out.Access, 300)
		out.Refresh = min(out.Refresh, 604800)
		out.IDToken = min(out.IDToken, 300)
		out.Code = min(out.Code, 60)
	}
	return out, nil
}
