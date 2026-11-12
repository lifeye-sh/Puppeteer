package identity

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/big"
	"next-terminal/backend/internal/platform/fault"
)

func randomInt(n int) int {
	v, e := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if e != nil {
		panic(e)
	}
	return int(v.Int64())
}
func pngData(img image.Image) string {
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}
func (s *Service) Captcha(ctx context.Context, binding string) (map[string]any, error) {
	if _, e := s.db.Exec(ctx, `DELETE FROM auth_challenges WHERE expires_at<now()`); e != nil {
		return nil, e
	}
	const w, h, size = 300, 140, 36
	x := 45 + randomInt(200)
	y := 35 + randomInt(60)
	bg := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(bg, bg.Bounds(), &image.Uniform{color.RGBA{70, 100, 155, 255}}, image.Point{}, draw.Src)
	for i := 0; i < 120; i++ {
		px, py := randomInt(w), randomInt(h)
		c := color.RGBA{byte(randomInt(200) + 40), byte(randomInt(200) + 40), byte(randomInt(200) + 40), 255}
		draw.Draw(bg, image.Rect(px, py, px+8+randomInt(35), py+8+randomInt(35)), &image.Uniform{c}, image.Point{}, draw.Src)
	}
	tile := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(tile, tile.Bounds(), bg, image.Pt(x, y), draw.Src)
	draw.Draw(bg, image.Rect(x, y, x+size, y+size), &image.Uniform{color.RGBA{20, 30, 50, 255}}, image.Point{}, draw.Src)
	key := NewToken()
	_, e := s.db.Exec(ctx, `INSERT INTO auth_challenges(id,purpose,binding_hash,answer,expires_at) VALUES($1,'login',$2,$3,now()+interval '2 minutes')`, key, Digest(binding), x)
	return map[string]any{"enabled": true, "key": key, "image": pngData(bg), "tile": pngData(tile), "width": w, "height": h, "tileWidth": size, "tileHeight": size, "tileY": y}, e
}
func (s *Service) VerifyCaptcha(ctx context.Context, key, binding string, x int) (string, error) {
	var answer int
	e := s.db.Row(ctx, `UPDATE auth_challenges SET consumed_at=now() WHERE id=$1 AND binding_hash=$2 AND purpose='login' AND expires_at>now() AND consumed_at IS NULL RETURNING answer`, key, Digest(binding)).Scan(&answer)
	if e != nil {
		return "", fault.Proof
	}
	if x < answer-3 || x > answer+3 {
		return "", fault.Proof
	}
	proof := NewToken()
	_, e = s.db.Exec(ctx, `INSERT INTO auth_challenges(id,purpose,binding_hash,answer,verified,expires_at) VALUES($1,'login-proof',$2,0,true,now()+interval '2 minutes')`, proof, Digest(binding))
	return proof, e
}
func (s *Service) ConsumeCaptcha(ctx context.Context, key, binding string) error {
	tag, e := s.db.Exec(ctx, `UPDATE auth_challenges SET consumed_at=now() WHERE id=$1 AND binding_hash=$2 AND purpose='login-proof' AND verified AND consumed_at IS NULL AND expires_at>now()`, key, Digest(binding))
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return fault.Proof
	}
	return nil
}
