package identity

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"net/mail"
	"next-terminal/backend/internal/platform/fault"
	"strings"
	"unicode"
	"unicode/utf8"
)

type LDAPEntry struct {
	Subject  string `json:"subject"`
	Username string `json:"username"`
	Nickname string `json:"nickname,omitempty"`
	Mail     string `json:"mail,omitempty"`
}

// Directory attributes are claims, not verified account contacts. Missing
// optional attributes are allowed; ambiguous/malformed values reject the whole
// preview so offboarding never consumes a partial directory.
func ldapMappedEntry(p LDAPProvider, entry *ldap.Entry, subject, username string) (LDAPEntry, error) {
	out := LDAPEntry{Subject: subject, Username: username}
	if p.PropertyMapping == nil {
		return out, nil
	}
	read := func(attr string, max int) (string, error) {
		if attr == "" {
			return "", nil
		}
		values := entry.GetEqualFoldAttributeValues(attr)
		if len(values) == 0 {
			return "", nil
		}
		if len(values) != 1 || !utf8.ValidString(values[0]) || utf8.RuneCountInString(values[0]) > max || strings.IndexFunc(values[0], unicode.IsControl) >= 0 {
			return "", ErrLDAPVerification
		}
		return values[0], nil
	}
	var e error
	if out.Nickname, e = read(p.PropertyMapping.Nickname, 256); e != nil {
		return out, e
	}
	if out.Mail, e = read(p.PropertyMapping.Mail, 254); e != nil {
		return out, e
	}
	if out.Mail != "" {
		address, e := mail.ParseAddress(out.Mail)
		if e != nil || address.Address != out.Mail || address.Name != "" {
			return out, ErrLDAPVerification
		}
	}
	return out, nil
}

func ReadLDAPDirectory(ctx context.Context, p LDAPProvider, secret string, limit int) ([]LDAPEntry, error) {
	if limit < 1 || limit > p.DirectoryLimit() || ValidateLDAPProvider(p) != nil {
		return nil, fault.Invalid
	}
	conn, cleanup, e := openLDAPConnection(ctx, p, secret, 16*1024*1024)
	if e != nil {
		return nil, e
	}
	defer cleanup()
	control := ldap.NewControlPaging(200)
	base, _ := ldap.ParseDN(p.BaseDN)
	out := []LDAPEntry{}
	seen := map[string]bool{}
	cookies := map[string]bool{}
	attributes := []string{p.IdentityAttribute, p.LoginAttribute}
	if m := p.PropertyMapping; m != nil {
		for _, attr := range []string{m.Nickname, m.Mail} {
			if attr != "" {
				attributes = append(attributes, attr)
			}
		}
	}
	for page := 0; page < 100; page++ {
		result, e := conn.Search(ldap.NewSearchRequest(p.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 5, false, p.SearchFilter, attributes, []ldap.Control{control}))
		if e != nil || len(result.Referrals) > 0 {
			return nil, ErrLDAPVerification
		}
		for _, entry := range result.Entries {
			dn, e := ldap.ParseDN(entry.DN)
			if e != nil || !(base.EqualFold(dn) || base.AncestorOfFold(dn)) {
				return nil, ErrLDAPVerification
			}
			values := entry.GetEqualFoldRawAttributeValues(p.IdentityAttribute)
			names := entry.GetEqualFoldAttributeValues(p.LoginAttribute)
			if len(values) != 1 || len(names) != 1 || names[0] == "" || len(names[0]) > 256 || !utf8.ValidString(names[0]) || strings.ContainsAny(names[0], "\x00\r\n") {
				return nil, ErrLDAPVerification
			}
			subject := ""
			if p.IdentityAttribute == "entryUUID" {
				id, e := uuid.Parse(string(values[0]))
				if e != nil || id == uuid.Nil {
					return nil, ErrLDAPVerification
				}
				subject = "entryUUID:" + id.String()
			} else {
				if len(values[0]) != 16 || string(values[0]) == string(make([]byte, 16)) {
					return nil, ErrLDAPVerification
				}
				subject = "objectGUID:" + hex.EncodeToString(values[0])
			}
			if seen[subject] {
				return nil, ErrLDAPVerification
			}
			seen[subject] = true
			mapped, e := ldapMappedEntry(p, entry, subject, names[0])
			if e != nil {
				return nil, e
			}
			out = append(out, mapped)
			if len(out) > limit {
				return nil, ErrLDAPVerification
			}
		}
		next := ldap.FindControl(result.Controls, ldap.ControlTypePaging)
		if next == nil {
			return out, nil
		}
		paging, ok := next.(*ldap.ControlPaging)
		if !ok {
			return nil, ErrLDAPVerification
		}
		if len(paging.Cookie) == 0 {
			return out, nil
		}
		cookie := string(paging.Cookie)
		if cookies[cookie] {
			return nil, ErrLDAPVerification
		}
		cookies[cookie] = true
		control.SetCookie(paging.Cookie)
	}
	return nil, ErrLDAPVerification
}
func (s *Service) SaveLDAPPreview(ctx context.Context, user, session string, p LDAPProvider, entries []LDAPEntry) (string, error) {
	token := NewToken()
	raw, _ := json.Marshal(entries)
	if _, e := s.db.Exec(ctx, `DELETE FROM ldap_sync_previews WHERE expires_at<=now() OR user_id=$1`, user); e != nil {
		return "", e
	}
	tag, e := s.db.Exec(ctx, `INSERT INTO ldap_sync_previews(token_hash,user_id,session_hash,provider_id,provider_version,auth_epoch,entries,expires_at) SELECT $1,$2,$3,p.id,p.version,i.auth_epoch,$6,now()+interval '5 minutes' FROM ldap_identity_providers p CROSS JOIN instance_state i WHERE p.id=$4 AND p.version=$5`, Digest(token), user, Digest(session), p.ID, p.Version, raw)
	if e != nil {
		return "", e
	}
	if tag.RowsAffected() != 1 {
		return "", fault.Proof
	}
	return token, nil
}
func (s *Service) ApplyLDAPPreview(ctx context.Context, user, session, token, department string, subjects []string, disableMissing bool) (map[string]int, error) {
	if len(token) != 64 || len(subjects) > 100 {
		return nil, fault.Invalid
	}
	var p string
	var version int64
	var raw []byte
	e := s.db.Row(ctx, `DELETE FROM ldap_sync_previews x USING instance_state i WHERE x.token_hash=$1 AND x.user_id=$2 AND x.session_hash=$3 AND x.auth_epoch=i.auth_epoch AND x.expires_at>now() RETURNING provider_id,provider_version,entries`, Digest(token), user, Digest(session)).Scan(&p, &version, &raw)
	if e != nil {
		return nil, fault.Proof
	}
	var current int64
	if e = s.db.Row(ctx, `SELECT version FROM ldap_identity_providers WHERE id=$1 FOR SHARE`, p).Scan(&current); e != nil || current != version {
		return nil, fault.Proof
	}
	if department != "" {
		var exists bool
		if e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM departments WHERE id=$1)`, department).Scan(&exists); e != nil || !exists {
			return nil, fault.Invalid
		}
	}
	var entries []LDAPEntry
	if json.Unmarshal(raw, &entries) != nil {
		return nil, fault.Proof
	}
	bySubject := map[string]LDAPEntry{}
	all := []string{}
	for _, entry := range entries {
		bySubject[entry.Subject] = entry
		all = append(all, entry.Subject)
	}
	counts := map[string]int{"created": 0, "existing": 0, "disabled": 0}
	for _, subject := range subjects {
		entry, ok := bySubject[subject]
		if !ok {
			return nil, fault.Invalid
		}
		var exists bool
		if e = s.db.Row(ctx, `SELECT EXISTS(SELECT 1 FROM ldap_identities WHERE provider_id=$1 AND subject=$2)`, p, subject).Scan(&exists); e != nil {
			return nil, e
		}
		if exists {
			counts["existing"]++
			continue
		}
		name := "ldap_" + strings.ReplaceAll(p, "-", "")[:8] + "_" + Digest(subject)[:16]
		display := entry.Nickname
		if strings.TrimSpace(display) == "" {
			display = entry.Username
		}
		nickname := []rune(display)
		if len(nickname) > 64 {
			nickname = nickname[:64]
		}
		var id string
		if e = s.db.Admit(ctx, "users", "identity.users"); e != nil {
			return nil, e
		}
		e = s.db.Row(ctx, `INSERT INTO users(username,nickname,password_hash,type,status) VALUES($1,$2,'!external-only','user','disabled') RETURNING id`, name, string(nickname)).Scan(&id)
		if e != nil {
			return nil, e
		}
		if _, e = s.db.Exec(ctx, `INSERT INTO ldap_identities(provider_id,user_id,subject,managed) VALUES($1,$2,$3,true)`, p, id, subject); e != nil {
			return nil, e
		}
		if department != "" {
			if _, e = s.db.Exec(ctx, `INSERT INTO user_departments(user_id,department_id) VALUES($1,$2)`, id, department); e != nil {
				return nil, e
			}
		}
		counts["created"]++
	}
	if disableMissing {
		rows, e := s.db.Rows(ctx, `SELECT u.id FROM ldap_identities e JOIN users u ON u.id=e.user_id WHERE e.provider_id=$1 AND e.managed AND NOT(e.subject=ANY($2::text[])) AND u.type<>'admin' AND u.id<>$3 AND u.status='enabled' ORDER BY u.id`, p, all, user)
		if e != nil {
			return nil, e
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return nil, e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
		for _, id := range ids {
			if e = s.LockUser(ctx, id); e != nil {
				return nil, e
			}
			tag, e := s.db.Exec(ctx, `UPDATE users SET status='disabled',version=version+1 WHERE id=$1 AND type<>'admin'`, id)
			if e != nil {
				return nil, e
			}
			if tag.RowsAffected() != 1 {
				continue
			}
			if e = s.RevokeAll(ctx, id); e != nil {
				return nil, e
			}
			counts["disabled"]++
		}
	}
	return counts, nil
}
