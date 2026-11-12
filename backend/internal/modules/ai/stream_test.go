package ai

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// Fragment every UTF-8 byte and every SSE field across independent reads.
type byteReader struct{ r io.Reader }

func (r byteReader) Read(b []byte) (int, error) { return r.r.Read(b[:1]) }
func TestStreamFramingAndFailures(t *testing.T) {
	content := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"你好\"},\"finish_reason\":null}]}\r\n\r\n"
	stop := "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"
	usage := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":2}}\n\n"
	var seen string
	reply, e := readStream(byteReader{strings.NewReader(": keepalive\n\n" + content + stop + usage + "data: [DONE]\n\n")}, func(v Reply) error { seen = v.Text; return nil })
	if e != nil || seen != "你好" || reply.Text != "你好" || reply.PromptTokens != 12 {
		t.Fatal(reply, e)
	}
	for name, raw := range map[string]string{
		"missing done": content + stop, "premature done": content + "data: [DONE]\n\n", "truncated done": content + stop + "data: [DONE]",
		"tool":         `data: {"choices":[{"delta":{"tool_calls":[{}]}}]}` + "\n\n",
		"length":       strings.Replace(content+stop+"data: [DONE]\n\n", `"stop"`, `"length"`, 1),
		"error":        content + "data: {\"error\":{\"message\":\"secret\"}}\n\n",
		"oversize":     "data: " + strings.Repeat("x", 262145) + "\n\n",
		"invalid utf8": "data: " + string([]byte{0xff}) + "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := readStream(strings.NewReader(raw), func(Reply) error { return nil }); e == nil {
				t.Fatal("accepted invalid stream")
			}
		})
	}
	sentinel := errors.New("cancelled")
	if _, e := readStream(strings.NewReader(content+stop), func(Reply) error { return sentinel }); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
}
