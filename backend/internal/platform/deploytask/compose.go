package deploytask

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Mapping is recorded only after explicit enrollment or successful deployment.
// Never infer ownership from a container port number alone.
type Mapping struct {
	Bind      string `json:"bind"`
	Published int    `json:"published"`
	Target    int    `json:"target"`
}
type ComposePlan struct {
	AuxiliaryPath    string          `json:"-"`
	AuxiliaryContent []byte          `json:"-"`
	Document         json.RawMessage `json:"-"` // may contain resolved secrets; never return to the API
	Previous         *Mapping        `json:"previous,omitempty"`
	Next             *Mapping        `json:"next,omitempty"`
}

// PlanCompose transforms docker compose config --format json output, preserving
// unrelated resolved configuration. Apply must use this as a standalone config,
// not a merge overlay (Compose port merging would retain removed mappings).
func PlanCompose(raw []byte, r Request, owned *Mapping) (ComposePlan, error) {
	if err := Validate(r); err != nil {
		return ComposePlan{}, err
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(raw, &doc) != nil {
		return ComposePlan{}, errors.New("invalid resolved Compose document")
	}
	var services map[string]map[string]json.RawMessage
	if json.Unmarshal(doc["services"], &services) != nil || services["server"] == nil {
		return ComposePlan{}, errors.New("server service missing")
	}
	server := services["server"]
	var networkMode string
	if v := server["network_mode"]; v != nil {
		if json.Unmarshal(v, &networkMode) != nil || networkMode != "" {
			return ComposePlan{}, errors.New("network_mode deployment is not supported")
		}
	}
	var ports []map[string]json.RawMessage
	if v := server["ports"]; v != nil && json.Unmarshal(v, &ports) != nil {
		return ComposePlan{}, errors.New("ports must be normalized by Compose")
	}
	target, _ := ContainerPort(r.Protocol)
	if owned != nil && owned.Target != target {
		return ComposePlan{}, errors.New("owned mapping does not match protocol")
	}
	remaining := make([]map[string]json.RawMessage, 0, len(ports)+1)
	found := 0
	for _, port := range ports {
		var t int
		if json.Unmarshal(port["target"], &t) != nil {
			return ComposePlan{}, errors.New("invalid port target")
		}
		if t == target {
			var protocol, host, published string
			json.Unmarshal(port["protocol"], &protocol)
			json.Unmarshal(port["host_ip"], &host)
			if json.Unmarshal(port["published"], &published) != nil {
				return ComposePlan{}, errors.New("unsupported published port")
			}
			if protocol == "" {
				protocol = "tcp"
			}
			if host == "" {
				host = "0.0.0.0"
			}
			if owned == nil || protocol != "tcp" || host != owned.Bind || published != strconv.Itoa(owned.Published) {
				return ComposePlan{}, errors.New("protocol has an unmanaged mapping; explicit enrollment required")
			}
			found++
			continue
		}
		remaining = append(remaining, port)
	}
	if owned != nil && found != 1 {
		return ComposePlan{}, errors.New("owned mapping drift detected")
	}
	// Check every service, including unrelated custom overlays, before adding a port.
	if r.Enabled {
		for name, service := range services {
			var existing []map[string]json.RawMessage
			if name == "server" {
				existing = remaining
			} else if value := service["ports"]; value != nil {
				if json.Unmarshal(value, &existing) != nil {
					return ComposePlan{}, errors.New("ports must be normalized by Compose")
				}
			}
			for _, port := range existing {
				var published, host, protocol string
				if port["published"] == nil {
					continue
				}
				if json.Unmarshal(port["published"], &published) != nil {
					return ComposePlan{}, errors.New("unsupported published port")
				}
				number, err := strconv.Atoi(published)
				if err != nil {
					return ComposePlan{}, errors.New("port ranges require explicit enrollment")
				}
				json.Unmarshal(port["host_ip"], &host)
				json.Unmarshal(port["protocol"], &protocol)
				if number == r.Port && (protocol == "" || protocol == "tcp") && (host == "" || host == "0.0.0.0" || host == "::" || r.Bind == "0.0.0.0" || host == r.Bind) {
					return ComposePlan{}, errors.New("requested host port conflicts with an existing service")
				}
			}
		}
	}
	var next *Mapping
	if r.Enabled {
		next = &Mapping{Bind: r.Bind, Published: r.Port, Target: target}
		port := map[string]json.RawMessage{}
		for key, value := range map[string]any{"target": target, "published": strconv.Itoa(r.Port), "host_ip": r.Bind, "protocol": "tcp"} {
			port[key], _ = json.Marshal(value)
		}
		remaining = append(remaining, port)
	}
	server["ports"], _ = json.Marshal(remaining)
	env := map[string]json.RawMessage{}
	if v := server["environment"]; v != nil && json.Unmarshal(v, &env) != nil {
		return ComposePlan{}, errors.New("environment must be normalized by Compose")
	}
	if env == nil {
		env = map[string]json.RawMessage{}
	}
	key := map[string]string{"mysql": "NT_DATABASE_PROXY_ADDR", "postgres": "NT_POSTGRES_PROXY_ADDR", "ssh": "NT_NATIVE_SSH_ADDR", "website": "NT_WEBSITE_ADDR"}[r.Protocol]
	address := ""
	if r.Enabled {
		address = fmt.Sprintf(":%d", target)
	}
	env[key], _ = json.Marshal(address)
	env["NT_DEPLOY_MANAGED_"+strings.ToUpper(r.Protocol)], _ = json.Marshal(strconv.FormatBool(r.Enabled))
	env["NT_DEPLOY_DISABLED_"+strings.ToUpper(r.Protocol)], _ = json.Marshal(strconv.FormatBool(!r.Enabled))
	if r.Protocol == "website" && r.Enabled {
		env["NT_WEBSITE_PUBLIC_PORT"], _ = json.Marshal(strconv.Itoa(r.Port))
	}
	server["environment"], _ = json.Marshal(env)
	doc["services"], _ = json.Marshal(services)
	result, err := json.Marshal(doc)
	var previous *Mapping
	if owned != nil {
		copy := *owned
		previous = &copy
	}
	return ComposePlan{Document: result, Previous: previous, Next: next}, err
}
