package vault

import (
	"context"
	"errors"
	"golang.org/x/crypto/ssh"
	"io"
	"regexp"
	"strings"
)

var ErrRotationPreflight = errors.New("rotation_preflight_failed")
var ErrRotationUnknown = errors.New("rotation_remote_result_unknown")
var linuxAccount = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// PasswordRotationCommand separates the fixed command from secret stdin. The
// caller must use a dedicated authenticated SSH connection and discard output.
func PasswordRotationCommand(username, password string, sudo bool) (string, io.Reader, error) {
	if !linuxAccount.MatchString(username) || len(password) < 20 || len(password) > 4096 || strings.ContainsAny(password, "\r\n\x00:") {
		return "", nil, ErrRotationPreflight
	}
	command := "chpasswd"
	if sudo {
		command = "sudo -n chpasswd"
	}
	return command, strings.NewReader(username + ":" + password + "\n"), nil
}

// ApplyLinuxPassword must only be called after the caller durably records
// 'applying' and checks its lease and authorization. It does not verify, activate
// or retry the candidate. Failure after Start is always ambiguous.
func ApplyLinuxPassword(ctx context.Context, client *ssh.Client, username, password string, sudo bool) error {
	command, stdin, e := PasswordRotationCommand(username, password, sudo)
	if e != nil {
		return e
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	session, e := client.NewSession()
	if e != nil {
		return ErrRotationPreflight
	}
	defer session.Close()
	session.Stdin = stdin
	session.Stdout = io.Discard
	session.Stderr = io.Discard
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stop()
	if e = session.Start(command); e != nil {
		return ErrRotationUnknown
	}
	if e = session.Wait(); e != nil || ctx.Err() != nil {
		return ErrRotationUnknown
	}
	return nil
}

// ReplaceAuthorizedKey builds a candidate authorized_keys file, preserving
// unrelated lines and options on the old key. The application must verify the
// new key before removing the old key in a separate explicit commit step.
func ReplaceAuthorizedKey(existing []byte, oldPublic, newPublic string, removeOld bool) ([]byte, error) {
	if len(existing) > 1<<20 || strings.ContainsRune(string(existing), 0) {
		return nil, ErrRotationPreflight
	}
	old, _, _, rest, e := ssh.ParseAuthorizedKey([]byte(oldPublic))
	if e != nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, ErrRotationPreflight
	}
	newKey, _, _, rest, e := ssh.ParseAuthorizedKey([]byte(newPublic))
	if e != nil || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, ErrRotationPreflight
	}
	if string(old.Marshal()) == string(newKey.Marshal()) {
		return nil, ErrRotationPreflight
	}
	lines := strings.Split(strings.TrimSuffix(string(existing), "\n"), "\n")
	var out []string
	foundOld, foundNew := false, false
	var oldOptions, newOptions string
	for _, line := range lines {
		key, _, options, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil {
			out = append(out, line)
			continue
		}
		if string(key.Marshal()) == string(newKey.Marshal()) {
			if foundNew {
				return nil, ErrRotationPreflight
			}
			foundNew = true
			newOptions = strings.Join(options, ",")
		}
		if string(key.Marshal()) != string(old.Marshal()) {
			out = append(out, line)
			continue
		}
		if foundOld {
			return nil, ErrRotationPreflight
		} // ambiguous duplicate restrictions
		foundOld = true
		oldOptions = strings.Join(options, ",")
		if !removeOld {
			out = append(out, line)
		}
		// Do not silently drop forced commands, from=, no-pty or forwarding restrictions.
		prefix := ""
		if len(options) > 0 {
			prefix = strings.Join(options, ",") + " "
		}
		if !removeOld {
			out = append(out, prefix+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(newKey))))
		}
	}
	if !foundOld || (!removeOld && foundNew) || (removeOld && !foundNew) {
		return nil, ErrRotationPreflight
	}
	if removeOld && oldOptions != newOptions {
		return nil, ErrRotationPreflight
	}
	return []byte(strings.Join(out, "\n") + "\n"), nil
}
