// Package systemupdate verifies release metadata before it can inform an update.
// It does not execute downloaded scripts or mutate a deployment.
package systemupdate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const IndexURL = "https://raw.githubusercontent.com/lifeye-sh/Puppeteer/main/deploy/production/updates/index.json"
const MaxIndexBytes = 1048576

type Envelope struct {
	KeyID     string `json:"keyId"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type Image struct {
	Component       string `json:"component"`
	Digest          string `json:"digest"`
	PinnedReference string `json:"pinnedReference"`
}
type Release struct {
	Version              string    `json:"version"`
	Channel              string    `json:"channel"`
	PublishedAt          time.Time `json:"publishedAt"`
	Platform             string    `json:"platform"`
	MinSource            string    `json:"minSource"`
	SchemaVersion        int       `json:"schemaVersion"`
	CapabilitiesRevision int       `json:"capabilitiesRevision"`
	NotesURL             string    `json:"notesUrl"`
	Images               []Image   `json:"images"`
}
type Catalog struct {
	Product   string    `json:"product"`
	Format    int       `json:"format"`
	Sequence  uint64    `json:"sequence"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Releases  []Release `json:"releases"`
}

var versionPattern = regexp.MustCompile(`^(?:beta(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})|r17-rc([1-9][0-9]{0,8})|v(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8}))$`)
var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func version(v string) ([4]int64, bool) {
	out := [4]int64{}
	if !versionPattern.MatchString(v) {
		return out, false
	}
	var parts []string
	switch {
	case strings.HasPrefix(v, "r17-rc"):
		parts = []string{"17", strings.TrimPrefix(v, "r17-rc")}
	case strings.HasPrefix(v, "beta"):
		out[0] = 1
		parts = strings.Split(v[4:], ".")
	default:
		out[0] = 2
		parts = strings.Split(v[1:], ".")
	}
	for i, s := range parts {
		out[i+1], _ = strconv.ParseInt(s, 10, 64)
	}
	return out, true
}
func Compare(a, b string) (int, error) {
	x, ok := version(a)
	y, ok2 := version(b)
	if !ok || !ok2 {
		return 0, errors.New("无法识别版本；自定义构建需要核验升级路径")
	}
	for i := range x {
		if x[i] < y[i] {
			return -1, nil
		}
		if x[i] > y[i] {
			return 1, nil
		}
	}
	return 0, nil
}
func strict(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}
func Validate(c Catalog, now time.Time) error {
	if c.Product != "Puppeteer" || c.Format != 1 || c.Sequence == 0 || c.IssuedAt.After(now.Add(5*time.Minute)) || !c.ExpiresAt.After(now) || !c.ExpiresAt.After(c.IssuedAt) || c.ExpiresAt.Sub(c.IssuedAt) > 90*24*time.Hour || len(c.Releases) > 200 {
		return errors.New("发布索引身份、格式或有效期无效")
	}
	seen := map[string]bool{}
	for _, r := range c.Releases {
		_, ok := version(r.Version)
		_, ok2 := version(r.MinSource)
		cmp, _ := Compare(r.MinSource, r.Version)
		channel := "beta"
		if strings.HasPrefix(r.Version, "v") {
			channel = "stable"
		}
		if !ok || !ok2 || cmp > 0 || r.Channel != channel || r.Platform != "linux/amd64" || r.SchemaVersion < 1 || r.CapabilitiesRevision < 1 || r.PublishedAt.IsZero() || r.PublishedAt.After(c.IssuedAt.Add(5*time.Minute)) || seen[r.Version] || r.NotesURL != "https://github.com/lifeye-sh/Puppeteer/releases/tag/"+r.Version || len(r.Images) != 4 {
			return errors.New("发布版本、升级路径或平台信息无效")
		}
		seen[r.Version] = true
		components := map[string]bool{}
		for _, im := range r.Images {
			if !(im.Component == "server" || im.Component == "web" || im.Component == "graphics" || im.Component == "renderer") || components[im.Component] || !digestPattern.MatchString(im.Digest) || im.PinnedReference != "cloners/puppeteer-"+im.Component+"@"+im.Digest {
				return errors.New("发布镜像清单无效")
			}
			components[im.Component] = true
		}
	}
	return nil
}
func Verify(raw []byte, keys map[string]ed25519.PublicKey, now time.Time, sequence uint64, priorDigest string) (Catalog, string, error) {
	var c Catalog
	var e Envelope
	if len(raw) > MaxIndexBytes || strict(raw, &e) != nil {
		return c, "", errors.New("发布索引格式或大小无效")
	}
	key := keys[e.KeyID]
	payload, err := base64.StdEncoding.Strict().DecodeString(e.Payload)
	sig, err2 := base64.StdEncoding.Strict().DecodeString(e.Signature)
	if err != nil || err2 != nil || len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, payload, sig) {
		return c, "", errors.New("发布索引签名无效或签名密钥不受信任")
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	if strict(payload, &c) != nil {
		return c, "", errors.New("发布索引内容无效")
	}
	if err = Validate(c, now); err != nil {
		return c, "", err
	}
	if c.Sequence < sequence || (c.Sequence == sequence && priorDigest != "" && digest != priorDigest) {
		return c, "", errors.New("发布索引发生回退或同序号内容变化")
	}
	return c, digest, nil
}
