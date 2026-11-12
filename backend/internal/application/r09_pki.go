package application

import (
	"context"
	"encoding/json"
	"net/http"
	"next-terminal/backend/internal/modules/identity"
	"next-terminal/backend/internal/modules/pki"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/website"
	"next-terminal/backend/internal/platform/fault"
	"strconv"
	"strings"
)

func (a *App) certificates() *pki.Service { return pki.New(a.db, a.config.VaultKey) }
func (a *App) websites() *website.Service { return website.New(a.db) }
func (a *App) routesR09PKI(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("GET /api/admin/certificates", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "certificate.read"); e != nil {
			return e
		}
		out, e := a.certificates().List(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, out)
		return nil
	})
	h("POST /api/admin/certificates", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Name     string   `json:"name"`
			Kind     string   `json:"kind"`
			Source   string   `json:"source"`
			Domains  []string `json:"domains"`
			Days     int      `json:"days"`
			PEM      string   `json:"pem"`
			Key      string   `json:"key"`
			ParentID string   `json:"parentId"`
			UserID   string   `json:"userId"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		var id string
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			var e error
			if in.Kind == "client" {
				if !resource.ValidID(in.ParentID) || !resource.ValidID(in.UserID) {
					return fault.Invalid
				}
				subject, e := a.identity.Subject(ctx, in.UserID)
				if e != nil {
					return e
				}
				if !subject.Enabled {
					return fault.Invalid
				}
				id, e = a.certificates().IssueClient(ctx, in.Name, in.ParentID, in.UserID, in.Days)
				if e != nil {
					return e
				}
			} else {
				cert, key := []byte(in.PEM), []byte(in.Key)
				defer clear(key)
				if in.Source == "self_signed" {
					cert, key, e = pki.Generate(in.Name, in.Domains, in.Days, in.Kind == "client_ca", nil)
					if e != nil {
						return e
					}
					defer clear(key)
				} else if in.Source != "import" {
					return fault.Invalid
				}
				id, e = a.certificates().Store(ctx, pki.Certificate{Name: in.Name, Kind: in.Kind, Source: in.Source, PEM: string(cert)}, key)
				if e != nil {
					return e
				}
			}
			return a.audit.Detail(ctx, u.ID, "certificate.created", requestID(r), sourceIP(r), map[string]string{"id": id, "kind": in.Kind})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	})
	h("POST /api/admin/certificates/{id}/revoke", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Version int64 `json:"version"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			if e := a.certificates().Revoke(ctx, r.PathValue("id"), in.Version); e != nil {
				return e
			}
			return a.audit.Detail(ctx, u.ID, "certificate.revoked", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
	h("POST /api/admin/certificates/{id}/p12", a.exportClientCertificate)
	h("POST /api/admin/certificates/{id}/reissue", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in struct {
			Version int64 `json:"version"`
			Days    int   `json:"days"`
		}
		if e = decode(w, r, &in); e != nil {
			return e
		}
		var id string
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			v, e := a.certificates().Get(ctx, r.PathValue("id"))
			if e != nil {
				return e
			}
			if v.Kind != "client" || v.Version != in.Version {
				return fault.Conflict
			}
			id, e = a.certificates().IssueClient(ctx, v.Name, v.ParentID, v.UserID, in.Days)
			if e != nil {
				return e
			}
			if v.RevokedAt == nil {
				if e = a.certificates().Revoke(ctx, v.ID, v.Version); e != nil {
					return e
				}
			}
			return a.audit.Detail(ctx, u.ID, "certificate.reissued", requestID(r), sourceIP(r), map[string]string{"id": id, "previousId": v.ID})
		})
		if e != nil {
			return e
		}
		writeJSON(w, 200, map[string]string{"id": id})
		return nil
	})
	h("GET /api/admin/website-settings", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		v, e := a.websites().Settings(r.Context())
		if e != nil {
			return e
		}
		writeJSON(w, 200, v)
		return nil
	})
	h("POST /api/admin/website-settings/preview", func(w http.ResponseWriter, r *http.Request) error {
		if _, e := a.require(r, "identity.manage"); e != nil {
			return e
		}
		var in website.Settings
		if e := decode(w, r, &in); e != nil {
			return e
		}
		sites, e := a.websites().List(r.Context())
		if e != nil {
			return e
		}
		impacts := []map[string]any{}
		for _, v := range sites {
			if v.CertificateID != "" {
				continue
			}
			valid := false
			if in.DefaultCertificateID != "" {
				pair, e := a.certificates().TLS(r.Context(), in.DefaultCertificateID)
				valid = e == nil && pair.Leaf.VerifyHostname(v.Domain) == nil
			}
			impacts = append(impacts, map[string]any{"id": v.ID, "name": v.Name, "domain": v.Domain, "certificateValid": valid})
		}
		writeJSON(w, 200, impacts)
		return nil
	})
	h("PUT /api/admin/website-settings", func(w http.ResponseWriter, r *http.Request) error {
		u, e := a.require(r, "identity.manage")
		if e != nil {
			return e
		}
		var in website.Settings
		if e = decode(w, r, &in); e != nil {
			return e
		}
		e = a.db.Within(r.Context(), func(ctx context.Context) error {
			// The unified adapter takes head before website_settings. Keep the same
			// order here so legacy and current editors cannot deadlock or overwrite
			// each other's proxy revision without a version conflict.
			var configurationID int64
			if e := a.db.Row(ctx, `SELECT revision FROM configuration_head WHERE singleton=1 FOR UPDATE`).Scan(&configurationID); e != nil {
				return e
			}
			previous, e := a.websites().Settings(ctx)
			if e != nil {
				return e
			}
			if in.DefaultCertificateID != "" {
				cert, e := a.certificates().Get(ctx, in.DefaultCertificateID)
				if e != nil {
					return e
				}
				if cert.Kind != "server" || cert.RevokedAt != nil {
					return fault.Invalid
				}
				pair, e := a.certificates().TLS(ctx, cert.ID)
				if e != nil {
					return e
				}
				sites, e := a.websites().List(ctx)
				if e != nil {
					return e
				}
				for _, v := range sites {
					if v.Enabled && v.CertificateID == "" && pair.Leaf.VerifyHostname(v.Domain) != nil {
						return problem(422, "certificate_domain", "默认服务器证书未覆盖已启用网站域名")
					}
				}
			}
			if in.DefaultCertificateID == "" {
				sites, err := a.websites().List(ctx)
				if err != nil {
					return err
				}
				for _, site := range sites {
					if site.Enabled && site.CertificateID == "" {
						return problem(422, "certificate_required", "启用的网站仍依赖默认服务器证书，不能清空")
					}
				}
			}
			if e := a.websites().SaveSettings(ctx, in); e != nil {
				return e
			}
			if strings.Join(previous.APITrustedProxies, ",") != strings.Join(in.APITrustedProxies, ",") {
				current, e := a.system.Configuration(ctx, configurationID)
				if e != nil {
					return e
				}
				header := current.Values.String("ip-extractor")
				if len(in.APITrustedProxies) == 0 {
					header = "direct"
				} else if header == "direct" {
					header = "x-forwarded-for"
				}
				trust, _ := json.Marshal(strings.Join(in.APITrustedProxies, ","))
				extractor, _ := json.Marshal(header)
				v, e := a.system.SaveConfiguration(ctx, nil, map[string]json.RawMessage{"ip-extractor": extractor, "ip-trust-list": trust}, u.ID)
				if e != nil {
					return e
				}
				if e = a.audit.Detail(ctx, u.ID, "settings.update", requestID(r), sourceIP(r), map[string]string{"revision": strconv.FormatInt(v.ID, 10), "legacy": "website-settings"}); e != nil {
					return e
				}
			}
			return a.audit.Detail(ctx, u.ID, "website.settings.saved", requestID(r), sourceIP(r), map[string]string{"version": strconv.FormatInt(in.Version, 10)})
		})
		if e != nil {
			return e
		}
		w.WriteHeader(204)
		return nil
	})
}
func (a *App) exportClientCertificate(w http.ResponseWriter, r *http.Request) error {
	if _, e := a.require(r, "identity.manage"); e != nil {
		return e
	}
	var in struct {
		Password       string `json:"password"`
		TOTP           string `json:"totp"`
		ExportPassword string `json:"exportPassword"`
	}
	if e := decode(w, r, &in); e != nil {
		return e
	}
	if len(in.Password) > 1024 {
		return fault.Invalid
	}
	release, e := a.hashPermit(r)
	if e != nil {
		return e
	}
	defer release()
	var data []byte
	e = a.securityTx(r, func(ctx context.Context, u *identity.User) error {
		if u.Type != "admin" || u.Phase != "authenticated" {
			return fault.Denied
		}
		verified, e := a.identity.Verify(ctx, u.Username, in.Password)
		if e != nil {
			return e
		}
		if verified == nil {
			return fault.Proof
		}
		enabled, _, e := a.identity.MFAStatus(ctx, u.ID)
		if e != nil {
			return e
		}
		if !enabled {
			return problem(403, "mfa_required", "请先绑定 TOTP 再导出客户端证书")
		}
		if e = a.identity.ConsumeTOTP(ctx, u.ID, in.TOTP); e != nil {
			return e
		}
		data, e = a.certificates().ExportClient(ctx, r.PathValue("id"), in.ExportPassword)
		if e != nil {
			return e
		}
		return a.audit.Detail(ctx, u.ID, "certificate.p12_exported", requestID(r), sourceIP(r), map[string]string{"id": r.PathValue("id")})
	})
	if e != nil {
		return e
	}
	defer clear(data)
	w.Header().Set("Content-Type", "application/x-pkcs12")
	w.Header().Set("Content-Disposition", `attachment; filename="client.p12"`)
	w.Header().Set("Cache-Control", "no-store")
	_, e = w.Write(data)
	return e
}
