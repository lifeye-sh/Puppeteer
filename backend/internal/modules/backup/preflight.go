package backup

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProbeRemote never creates directories or objects. Write/readback/delete is a
// separate explicit verification because it mutates the destination.
func ProbeRemote(ctx context.Context, d Destination) error {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	r, err := openRemote(ctx, d)
	if err != nil {
		return errors.New("backup dependency failed")
	}
	defer r.Close()
	switch s := r.(type) {
	case *sftpStore:
		prefix := s.prefix
		if prefix == "" {
			prefix = "."
		}
		info, e := s.fs.Stat(prefix)
		if e == nil && info.IsDir() {
			return nil
		}
	case *s3Store:
		exists, e := s.c.BucketExists(ctx, s.bucket)
		if e == nil && exists {
			return nil
		}
	case *davStore:
		// Many DAV servers reject HEAD on collections. A depth-zero PROPFIND
		// reads only the selected directory's type, never enumerating its files.
		req, e := http.NewRequestWithContext(ctx, "PROPFIND", s.root.String(), strings.NewReader(`<propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`))
		if e != nil {
			break
		}
		req.Header.Set("Depth", "0")
		req.Header.Set("Content-Type", "application/xml")
		if s.user != "" {
			req.SetBasicAuth(s.user, s.secret)
		}
		res, e := s.client.Do(req)
		if e != nil {
			break
		}
		defer res.Body.Close()
		data, e := io.ReadAll(io.LimitReader(res.Body, 128*1024+1))
		if e != nil || len(data) > 128*1024 || res.StatusCode != 207 {
			break
		}
		var body struct {
			Responses []struct {
				Props []struct {
					Status string `xml:"status"`
					Prop   struct {
						Type struct {
							Collection *struct{} `xml:"collection"`
						} `xml:"resourcetype"`
					} `xml:"prop"`
				} `xml:"propstat"`
			} `xml:"response"`
		}
		if xml.Unmarshal(data, &body) == nil && len(body.Responses) == 1 {
			for _, p := range body.Responses[0].Props {
				fields := strings.Fields(p.Status)
				if len(fields) >= 2 && fields[1] == "200" && p.Prop.Type.Collection != nil {
					return nil
				}
			}
		}
	}
	return errors.New("backup path, authentication or TLS check failed")
}
