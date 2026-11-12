//go:build !windows && !linux

package nativeclient

// No plaintext or command-line secret fallback on unsupported platforms.
func (c *Client) SystemCredentialStore() (CredentialStore, error) {
	return nil, ErrCredentialUnavailable
}
