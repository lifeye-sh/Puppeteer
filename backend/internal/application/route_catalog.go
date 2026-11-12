package application

import (
	"sort"
	"strings"
)

type HTTPRoute struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Fallback bool   `json:"fallback,omitempty"`
}

// RegisteredHTTPRoutes executes registration only, never a request handler. It
// needs no database, credentials, sockets or network. This proves registration,
// not authorization, business completeness or client compatibility.
func RegisteredHTTPRoutes() []HTTPRoute {
	routes := []HTTPRoute{}
	a := &App{}
	a.handler(func(pattern string) {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok {
			method, path = "*", pattern
		}
		routes = append(routes, HTTPRoute{Method: method, Path: path, Fallback: !ok})
	})
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].Path != routes[j].Path {
			return routes[i].Path < routes[j].Path
		}
		return routes[i].Method < routes[j].Method
	})
	return routes
}
