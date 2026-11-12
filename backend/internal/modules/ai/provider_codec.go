package ai

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

func requestPayload(p Profile, messages []Message, stream bool) (string, map[string]any) {
	payload := map[string]any{"model": p.Model, "stream": stream}
	route := "/chat/completions"
	definitions := []any{}
	switch p.Protocol {
	case "responses":
		route = "/responses"
		payload["input"] = messages
		payload["max_output_tokens"] = p.MaxOutputTokens
		payload["store"] = false
		for _, d := range p.Tools {
			definitions = append(definitions, map[string]any{"type": "function", "name": d.Name, "description": d.Description, "parameters": d.Parameters, "strict": false})
		}
	case "anthropic":
		route = "/messages"
		system := []string{}
		conversation := []Message{}
		for _, m := range messages {
			if m.Role == "system" {
				system = append(system, m.Content)
			} else {
				conversation = append(conversation, m)
			}
		}
		payload["system"] = strings.Join(system, "\n")
		payload["messages"] = conversation
		payload["max_tokens"] = p.MaxOutputTokens
		for _, d := range p.Tools {
			definitions = append(definitions, map[string]any{"name": d.Name, "description": d.Description, "input_schema": d.Parameters})
		}
	default:
		payload["messages"] = messages
		payload["max_tokens"] = p.MaxOutputTokens
		if stream {
			payload["stream_options"] = map[string]bool{"include_usage": true}
		}
		for _, d := range p.Tools {
			definitions = append(definitions, map[string]any{"type": "function", "function": d})
		}
	}
	if len(definitions) > 0 {
		payload["tools"] = definitions
	}
	if p.Temperature != nil {
		payload["temperature"] = *p.Temperature
	}
	return route, payload
}
func validateReply(r Reply, defs []ToolDefinition) error {
	if (r.Text == "" && len(r.ToolCalls) == 0) || len(r.Text) > 131072 || !utf8.ValidString(r.Text) || r.PromptTokens < 0 || r.CompletionTokens < 0 || r.PromptTokens > 10000000 || r.CompletionTokens > 1000000 || !validCalls(r.ToolCalls, defs) {
		return ErrProvider
	}
	return nil
}
func parseCompletion(raw []byte, p Profile) (r Reply, e error) {
	var v struct {
		Status     string          `json:"status"`
		Error      json.RawMessage `json:"error"`
		StopReason string          `json:"stop_reason"`
		Choices    []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Output []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage *struct {
			Prompt        int `json:"prompt_tokens"`
			Completion    int `json:"completion_tokens"`
			Input         int `json:"input_tokens"`
			Output        int `json:"output_tokens"`
			CacheRead     int `json:"cache_read_input_tokens"`
			CacheCreation int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if !utf8.Valid(raw) || json.Unmarshal(raw, &v) != nil || (len(v.Error) > 0 && string(v.Error) != "null") {
		return r, ErrProvider
	}
	switch p.Protocol {
	case "responses":
		if v.Status != "completed" {
			return r, ErrProvider
		}
		for _, o := range v.Output {
			if o.Type == "message" {
				for _, c := range o.Content {
					if c.Type == "output_text" {
						r.Text += c.Text
					}
				}
			} else if o.Type == "function_call" {
				r.ToolCalls = append(r.ToolCalls, ToolCall{o.CallID, o.Name, o.Arguments})
			}
		}
	case "anthropic":
		if v.StopReason != "end_turn" && v.StopReason != "tool_use" {
			return r, ErrProvider
		}
		for _, c := range v.Content {
			if c.Type == "text" {
				r.Text += c.Text
			} else if c.Type == "tool_use" {
				r.ToolCalls = append(r.ToolCalls, ToolCall{c.ID, c.Name, string(c.Input)})
			}
		}
	default:
		if len(v.Choices) != 1 {
			return r, ErrProvider
		}
		r.Text = v.Choices[0].Message.Content
		for _, c := range v.Choices[0].Message.ToolCalls {
			r.ToolCalls = append(r.ToolCalls, ToolCall{c.ID, c.Function.Name, c.Function.Arguments})
		}
	}
	if v.Usage != nil {
		r.UsageKnown = true
		r.PromptTokens = v.Usage.Prompt
		r.CompletionTokens = v.Usage.Completion
		if p.Protocol == "responses" || p.Protocol == "anthropic" {
			r.PromptTokens = v.Usage.Input + v.Usage.CacheRead + v.Usage.CacheCreation
			r.CompletionTokens = v.Usage.Output
		}
	}
	return r, validateReply(r, p.Tools)
}
