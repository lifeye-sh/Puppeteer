package system

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"next-terminal/backend/internal/platform/fault"
)

// Accept bounded raster inputs, then re-encode so metadata, scripts and trailing
// polyglot content are never served. SVG, GIF, URLs and arbitrary HTML are rejected.
func SanitizeLogo(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > 1024*1024 {
		return nil, fault.Invalid
	}
	config, kind, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || (kind != "png" && kind != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 1024 || config.Height > 1024 {
		return nil, fault.Invalid
	}
	decoded, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fault.Invalid
	}
	var output bytes.Buffer
	if err = png.Encode(&output, decoded); err != nil || output.Len() > 2*1024*1024 {
		return nil, fault.Invalid
	}
	return output.Bytes(), nil
}
func (s *Service) StoreLogo(ctx context.Context, data []byte, actor string) (string, error) {
	if !s.db.InTransaction(ctx) || len(data) == 0 || len(data) > 2*1024*1024 {
		return "", fault.Invalid
	}
	if _, err := s.db.Exec(ctx, `SELECT pg_advisory_xact_lock(680018)`); err != nil {
		return "", err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	var id string
	if err := s.db.Row(ctx, `SELECT coalesce((SELECT id::text FROM brand_images WHERE sha256=$1),'')`, hash).Scan(&id); err != nil {
		return "", err
	}
	if id != "" {
		return id, nil
	}
	// Remove abandoned uploads only. Every image referenced by history is retained.
	if _, err := s.db.Exec(ctx, `DELETE FROM brand_images b WHERE created_at<now()-interval '1 day' AND NOT EXISTS(SELECT 1 FROM configuration_revisions r WHERE r.snapshot->>'system-logo'=b.id::text)`); err != nil {
		return "", err
	}
	var count int
	if err := s.db.Row(ctx, `SELECT count(*) FROM brand_images`).Scan(&count); err != nil {
		return "", err
	}
	if count >= 100 {
		return "", fault.Conflict
	}
	err := s.db.Row(ctx, `INSERT INTO brand_images(sha256,data,created_by) VALUES($1,$2,$3) RETURNING id`, hash, data, actor).Scan(&id)
	return id, err
}
func (s *Service) Logo(ctx context.Context, id string) ([]byte, error) {
	var data []byte
	err := s.db.Row(ctx, `SELECT data FROM brand_images WHERE id=$1`, id).Scan(&data)
	return data, err
}
