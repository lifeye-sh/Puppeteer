package deploytask

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
)

type composeModel struct {
	Services map[string]map[string]json.RawMessage `json:"services"`
}
type composeMount struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

func nginxSource(raw []byte) (string, error) {
	var model composeModel
	if json.Unmarshal(raw, &model) != nil {
		return "", errors.New("invalid Compose document")
	}
	var mounts []composeMount
	if json.Unmarshal(model.Services["web"]["volumes"], &mounts) != nil {
		return "", errors.New("web bind-mounted Nginx configuration required")
	}
	found := ""
	for _, mount := range mounts {
		if mount.Target == "/etc/nginx/nginx.conf" {
			if found != "" || mount.Type != "bind" || !mount.ReadOnly || !filepath.IsAbs(mount.Source) {
				return "", errors.New("unsupported Nginx configuration mount")
			}
			found = mount.Source
		}
	}
	if found == "" {
		return "", errors.New("Nginx configuration mount missing")
	}
	return found, nil
}

// Only the standard single server upstream is rewritten. Custom/ambiguous
// upstream definitions fail preflight rather than being guessed or overwritten.
func planManagement(raw, nginx []byte, r Request, dir string) (ComposePlan, error) {
	if err := Validate(r); err != nil {
		return ComposePlan{}, err
	}
	if r.Protocol != "management" {
		return ComposePlan{}, errors.New("management request required")
	}
	expression := regexp.MustCompile(`(?m)^([ \t]*proxy_pass[ \t]+http://server:)[0-9]+(;[ \t]*\r?)$`)
	if len(expression.FindAll(nginx, -1)) != 1 {
		return ComposePlan{}, errors.New("Nginx requires exactly one standard server upstream for automatic management-port changes")
	}
	updated := expression.ReplaceAllFunc(nginx, func(line []byte) []byte {
		parts := expression.FindSubmatch(line)
		return []byte(fmt.Sprintf("%s%d%s", parts[1], r.Port, parts[2]))
	})
	digest := sha256.Sum256(updated)
	path := filepath.Join(dir, fmt.Sprintf("nginx-%x.conf", digest[:12]))
	var doc map[string]json.RawMessage
	var model composeModel
	if json.Unmarshal(raw, &doc) != nil || json.Unmarshal(raw, &model) != nil {
		return ComposePlan{}, errors.New("invalid Compose document")
	}
	server := model.Services["server"]
	web := model.Services["web"]
	if server == nil || web == nil {
		return ComposePlan{}, errors.New("server/web service missing")
	}
	var ports []struct{ Target int }
	if value := server["ports"]; value != nil {
		if json.Unmarshal(value, &ports) != nil {
			return ComposePlan{}, errors.New("invalid server ports")
		}
	}
	for _, port := range ports {
		if port.Target == r.Port {
			return ComposePlan{}, errors.New("management port conflicts with an existing published service")
		}
	}
	for _, protocol := range []string{"mysql", "postgres", "ssh", "website"} {
		port, _ := ContainerPort(protocol)
		if r.Port == port {
			return ComposePlan{}, errors.New("management port conflicts with a reserved protocol listener")
		}
	}
	env := map[string]string{}
	if value := server["environment"]; value != nil && json.Unmarshal(value, &env) != nil {
		return ComposePlan{}, errors.New("unsupported server environment")
	}
	env["NT_ADDR"] = fmt.Sprintf("0.0.0.0:%d", r.Port)
	env["NT_DEPLOY_MANAGED_HTTP_ADDR"] = env["NT_ADDR"]
	server["environment"], _ = json.Marshal(env)
	var mounts []map[string]json.RawMessage
	if json.Unmarshal(web["volumes"], &mounts) != nil {
		return ComposePlan{}, errors.New("invalid web mounts")
	}
	found := false
	for _, mount := range mounts {
		var target string
		json.Unmarshal(mount["target"], &target)
		if target == "/etc/nginx/nginx.conf" {
			mount["source"], _ = json.Marshal(path)
			found = true
		}
	}
	if !found {
		return ComposePlan{}, errors.New("Nginx mount missing")
	}
	web["volumes"], _ = json.Marshal(mounts)
	doc["services"], _ = json.Marshal(model.Services)
	document, err := json.Marshal(doc)
	return ComposePlan{Document: document, AuxiliaryPath: path, AuxiliaryContent: updated}, err
}
