package ai

import "encoding/json"

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

func ToolDefinitions(shell, audit bool) []ToolDefinition {
	out := []ToolDefinition{}
	add := func(name, description string, properties map[string]any, required []string) {
		out = append(out, ToolDefinition{Name: name, Description: description, Parameters: map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}})
	}
	stringField := func(description string) any { return map[string]any{"type": "string", "description": description} }
	add("asset_inspect", "Read only the user-selected asset metadata; requires explicit user approval.", map[string]any{}, []string{})
	if audit {
		add("audit_commands", "Show command audit for the selected session locally. Results are not sent back to the model automatically.", map[string]any{}, []string{})
	}
	if shell {
		add("session_command", "Propose an SSH command on the selected connected session. Never runs until the user approves the exact command. Output remains local.", map[string]any{"command": stringField("Exact shell command, at most 4096 bytes")}, []string{"command"})
		add("file_read", "Read a UTF-8 file through the existing SFTP service after user approval. Contents remain local.", map[string]any{"path": stringField("Absolute path within the SFTP server's exposed filesystem")}, []string{"path"})
		add("file_write", "Propose writing UTF-8 content through SFTP. User must preview existing content and approve the exact diff; optimistic file hash protects concurrent edits.", map[string]any{"path": stringField("Absolute path"), "content": stringField("Complete new file content; maximum 8192 bytes")}, []string{"path", "content"})
	}
	return out
}
func validCalls(calls []ToolCall, defs []ToolDefinition) bool {
	if len(calls) > 4 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range calls {
		if c.ID == "" || len(c.ID) > 200 || seen[c.ID] || len(c.Arguments) > 24576 || !json.Valid([]byte(c.Arguments)) {
			return false
		}
		seen[c.ID] = true
		allowed := false
		for _, d := range defs {
			if d.Name == c.Name {
				allowed = true
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}
