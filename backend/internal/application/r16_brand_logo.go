package application

import (
	"context"
	"io"
	"net/http"
	"next-terminal/backend/internal/modules/resource"
	"next-terminal/backend/internal/modules/system"
	"next-terminal/backend/internal/platform/fault"
)

func (a *App) routesR16BrandLogo(h func(string, func(http.ResponseWriter, *http.Request) error)) {
	h("POST /api/admin/branding/logo", func(w http.ResponseWriter, r *http.Request) error {
		u, err := a.require(r, "identity.manage")
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
		if err != nil {
			return problem(413, "logo_too_large", "Logo 文件不能超过 1 MiB")
		}
		data, err := system.SanitizeLogo(raw)
		if err != nil {
			return problem(422, "invalid_logo", "请选择不超过 1024×1024 的 PNG 或 JPEG 图片")
		}
		var id string
		err = a.db.Within(r.Context(), func(c context.Context) error {
			var e error
			id, e = a.system.StoreLogo(c, data, u.ID)
			if e != nil {
				return e
			}
			return a.audit.Detail(c, u.ID, "branding.logo.upload", requestID(r), sourceIP(r), map[string]string{"imageId": id})
		})
		if err != nil {
			return err
		}
		writeJSON(w, 201, map[string]string{"id": id, "previewUrl": "/api/admin/branding/logo/" + id})
		return nil
	})
	h("GET /api/admin/branding/logo/{id}", func(w http.ResponseWriter, r *http.Request) error {
		if _, err := a.require(r, "identity.manage"); err != nil {
			return err
		}
		id := r.PathValue("id")
		if !resource.ValidID(id) {
			return fault.Invalid
		}
		data, err := a.system.Logo(r.Context(), id)
		if err != nil {
			return err
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		_, err = w.Write(data)
		return err
	})
}
