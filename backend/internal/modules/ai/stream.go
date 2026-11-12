package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"unicode/utf8"
)

func GenerateStream(ctx context.Context, p Profile, messages []Message, progress func(Reply) error) (Reply, error) {
	res, cleanup, e := providerRequest(ctx, p, messages, true)
	if e != nil {
		return Reply{}, e
	}
	defer cleanup()
	if !strings.HasPrefix(strings.ToLower(res.Header.Get("Content-Type")), "text/event-stream") {
		return Reply{}, ErrProvider
	}
	return readProtocolStream(res.Body, p, progress)
}
func readStream(body io.Reader, progress func(Reply) error) (Reply, error) {
	return readProtocolStream(body, Profile{}, progress)
}
func readProtocolStream(body io.Reader, p Profile, progress func(Reply) error) (reply Reply, err error) {
	scanner := bufio.NewScanner(io.LimitReader(body, 4<<20))
	scanner.Buffer(make([]byte, 4096), 262144)
	var data strings.Builder
	finished, usageSeen := false, false
	calls := map[int]ToolCall{}
	appendCall := func(index int, id, name, args string) error {
		if index < 0 || index > 64 {
			return ErrProvider
		}
		c := calls[index]
		c.ID += id
		c.Name += name
		c.Arguments += args
		if len(c.ID) > 200 || len(c.Name) > 100 || len(c.Arguments) > 24576 {
			return ErrProvider
		}
		calls[index] = c
		if len(calls) > 4 {
			return ErrProvider
		}
		return nil
	}
	finalize := func() error {
		indices := []int{}
		for i := range calls {
			indices = append(indices, i)
		}
		sort.Ints(indices)
		reply.ToolCalls = nil
		for _, i := range indices {
			reply.ToolCalls = append(reply.ToolCalls, calls[i])
		}
		reply.UsageKnown = usageSeen
		return validateReply(reply, p.Tools)
	}
	consume := func() (bool, error) {
		raw := strings.TrimSuffix(data.String(), "\n")
		data.Reset()
		if raw == "" {
			return false, nil
		}
		if raw == "[DONE]" {
			if p.Protocol == "anthropic" || p.Protocol == "responses" || !finished {
				return false, ErrProvider
			}
			return true, finalize()
		}
		if !utf8.ValidString(raw) {
			return false, ErrProvider
		}
		var v struct {
			Type        string          `json:"type"`
			Index       int             `json:"index"`
			OutputIndex int             `json:"output_index"`
			Delta       json.RawMessage `json:"delta"`
			Error       json.RawMessage `json:"error"`
			Choices     []struct {
				Index int `json:"index"`
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
					FunctionCall json.RawMessage `json:"function_call"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				Prompt     int `json:"prompt_tokens"`
				Completion int `json:"completion_tokens"`
				Input      int `json:"input_tokens"`
				Output     int `json:"output_tokens"`
			} `json:"usage"`
			Message struct {
				Usage struct {
					Input    int `json:"input_tokens"`
					Output   int `json:"output_tokens"`
					Read     int `json:"cache_read_input_tokens"`
					Creation int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type  string         `json:"type"`
				Text  string         `json:"text"`
				ID    string         `json:"id"`
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
			} `json:"content_block"`
			Item struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"item"`
			Response json.RawMessage `json:"response"`
		}
		if json.Unmarshal([]byte(raw), &v) != nil || (len(v.Error) > 0 && string(v.Error) != "null") {
			return false, ErrProvider
		}
		before := reply.Text
		switch p.Protocol {
		case "anthropic":
			switch v.Type {
			case "message_start":
				reply.PromptTokens = v.Message.Usage.Input + v.Message.Usage.Read + v.Message.Usage.Creation
				reply.CompletionTokens = v.Message.Usage.Output
			case "content_block_start":
				if v.ContentBlock.Type == "text" {
					reply.Text += v.ContentBlock.Text
				} else if v.ContentBlock.Type == "tool_use" {
					args := ""
					if len(v.ContentBlock.Input) > 0 {
						b, _ := json.Marshal(v.ContentBlock.Input)
						args = string(b)
					}
					if e := appendCall(v.Index, v.ContentBlock.ID, v.ContentBlock.Name, args); e != nil {
						return false, e
					}
				}
			case "content_block_delta":
				var d struct {
					Type    string `json:"type"`
					Text    string `json:"text"`
					Partial string `json:"partial_json"`
				}
				if json.Unmarshal(v.Delta, &d) != nil {
					return false, ErrProvider
				}
				if d.Type == "text_delta" {
					reply.Text += d.Text
				} else if d.Type == "input_json_delta" {
					if _, ok := calls[v.Index]; !ok {
						return false, ErrProvider
					}
					if e := appendCall(v.Index, "", "", d.Partial); e != nil {
						return false, e
					}
				}
			case "message_delta":
				var d struct {
					Stop string `json:"stop_reason"`
				}
				if json.Unmarshal(v.Delta, &d) != nil {
					return false, ErrProvider
				}
				if d.Stop != "" {
					if d.Stop != "end_turn" && d.Stop != "tool_use" {
						return false, ErrProvider
					}
					finished = true
				}
				if v.Usage != nil {
					reply.CompletionTokens = v.Usage.Output
					usageSeen = true
				}
			case "message_stop":
				if !finished {
					return false, ErrProvider
				}
				for i, c := range calls {
					if c.Arguments == "" {
						c.Arguments = "{}"
						calls[i] = c
					}
				}
				return true, finalize()
			case "error":
				return false, ErrProvider
			}
		case "responses":
			switch v.Type {
			case "response.output_text.delta":
				var delta string
				if json.Unmarshal(v.Delta, &delta) != nil {
					return false, ErrProvider
				}
				reply.Text += delta
			case "response.output_item.added":
				if v.Item.Type == "function_call" {
					if e := appendCall(v.OutputIndex, v.Item.CallID, v.Item.Name, v.Item.Arguments); e != nil {
						return false, e
					}
				}
			case "response.function_call_arguments.delta":
				var delta string
				if json.Unmarshal(v.Delta, &delta) != nil {
					return false, ErrProvider
				}
				if _, ok := calls[v.OutputIndex]; !ok {
					return false, ErrProvider
				}
				if e := appendCall(v.OutputIndex, "", "", delta); e != nil {
					return false, e
				}
			case "response.completed":
				final, e := parseCompletion(v.Response, p)
				if e != nil {
					return false, e
				}
				if reply.Text != "" && reply.Text != final.Text {
					return false, ErrProvider
				}
				reply = final
				return true, nil
			case "error", "response.failed", "response.incomplete":
				return false, ErrProvider
			}
		default:
			if len(v.Choices) > 1 {
				return false, ErrProvider
			}
			if v.Usage != nil {
				reply.PromptTokens = v.Usage.Prompt
				reply.CompletionTokens = v.Usage.Completion
				usageSeen = true
			}
			if len(v.Choices) == 0 {
				if v.Usage == nil {
					return false, ErrProvider
				}
				break
			}
			c := v.Choices[0]
			if c.Index != 0 || finished || (len(c.Delta.FunctionCall) > 0 && string(c.Delta.FunctionCall) != "null") {
				return false, ErrProvider
			}
			reply.Text += c.Delta.Content
			for _, call := range c.Delta.ToolCalls {
				if e := appendCall(call.Index, call.ID, call.Function.Name, call.Function.Arguments); e != nil {
					return false, e
				}
			}
			if c.FinishReason != nil {
				if *c.FinishReason != "stop" && *c.FinishReason != "tool_calls" {
					return false, ErrProvider
				}
				if *c.FinishReason == "tool_calls" && len(calls) == 0 {
					return false, ErrProvider
				}
				finished = true
			}
		}
		if len(reply.Text) > 131072 || reply.PromptTokens < 0 || reply.CompletionTokens < 0 || reply.PromptTokens > 10000000 || reply.CompletionTokens > 1000000 {
			return false, ErrProvider
		}
		if before != reply.Text {
			if e := progress(reply); e != nil {
				return false, e
			}
		}
		return false, nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			done, e := consume()
			if e != nil {
				return reply, e
			}
			if done {
				return reply, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if data.Len()+len(value) > 262144 {
				return reply, ErrProvider
			}
			data.WriteString(value)
			data.WriteByte('\n')
		}
	}
	return reply, ErrProvider
}
