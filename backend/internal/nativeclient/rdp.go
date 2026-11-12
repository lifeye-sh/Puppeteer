package nativeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var viewTicket = regexp.MustCompile("^NTICKET:[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}:[a-f0-9]{64}$")

func (c *Client) RDPFile(ctx context.Context, t Token, asset string) ([]byte, error) {
	body, err := json.Marshal(map[string]string{"assetId": asset})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.origin+"/api/native/v1/rdp-file", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.Access)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errors.New("recorded desktop request denied or unavailable: " + res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 2049))
	if err != nil || len(data) > 2048 {
		return nil, errors.New("invalid desktop response")
	}
	return sanitizeDesktopFile(string(data))
}
func sanitizeDesktopFile(data string) ([]byte, error) {
	var addr, ticket string
	for _, line := range strings.Split(data, "\r\n") {
		if v, ok := strings.CutPrefix(line, "full address:s:"); ok {
			if addr != "" {
				return nil, errors.New("duplicate endpoint")
			}
			addr = v
		}
		if v, ok := strings.CutPrefix(line, "username:s:"); ok {
			if ticket != "" {
				return nil, errors.New("duplicate ticket")
			}
			ticket = v
		}
	}
	host, port, err := net.SplitHostPort(addr)
	n, e := strconv.Atoi(port)
	if err != nil || e != nil || n < 1 || n > 65535 || (!net.ParseIP(host).IsLoopback() && host != "backend.localhost") || !viewTicket.MatchString(ticket) {
		return nil, errors.New("invalid recorded desktop endpoint or ticket")
	}
	// Reconstruct only the approved client settings. Remote content cannot add
	// drive sharing, credential forwarding or a RemoteApp launch directive.
	return []byte("full address:s:" + net.JoinHostPort(host, strconv.Itoa(n)) + "\r\nusername:s:" + ticket + "\r\nauthentication level:i:1\r\nenablecredsspsupport:i:0\r\nnegotiate security layer:i:1\r\nscreen mode id:i:1\r\ndesktopwidth:i:1280\r\ndesktopheight:i:720\r\nredirectclipboard:i:0\r\ndrivestoredirect:s:\r\nredirectprinters:i:0\r\nredirectsmartcards:i:0\r\naudiomode:i:2\r\n"), nil
}
