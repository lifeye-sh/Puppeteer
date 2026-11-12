package nativeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// PrepareAccess obtains a proof bound to this OAuth family before reserving a
// remote session. The server remains authoritative at reservation time.
func (c *Client) PrepareAccess(ctx context.Context, token Token, prompt func() (string, error)) error {
	type status struct {
		Required bool `json:"required"`
		Verified bool `json:"verified"`
		Enrolled bool `json:"enrolled"`
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.origin+"/api/native/v1/access-mfa", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token.Access)
	var state status
	if err = c.do(req, &state); err != nil {
		return err
	}
	if !state.Required || state.Verified {
		return nil
	}
	if !state.Enrolled {
		return errors.New("请先在平台个人账号中绑定动态口令，再连接远程资产")
	}
	if prompt == nil {
		return errors.New("连接远程资产需要动态口令验证")
	}
	code, err := prompt()
	if err != nil {
		return err
	}
	if len(code) != 6 {
		return errors.New("动态口令必须为六位数字")
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return errors.New("动态口令必须为六位数字")
		}
	}
	body, _ := json.Marshal(map[string]string{"totp": code})
	code = ""
	defer clear(body)
	req, err = http.NewRequestWithContext(ctx, "POST", c.origin+"/api/native/v1/access-mfa", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token.Access)
	req.Header.Set("Content-Type", "application/json")
	if err = c.do(req, &state); err != nil {
		return err
	}
	if !state.Verified {
		return errors.New("接入验证未生效，请重新验证")
	}
	return nil
}
