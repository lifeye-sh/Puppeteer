package audit

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// InputTracker correlates submissions with visible PTY echo. It is not a shell
// interpreter. Never persist raw input: it can contain passwords with echo off.
// The transport serializes Input/Output with its recording mutex.
type InputTracker struct {
	screen                                      echoLine
	line                                        []rune
	cursor                                      int
	prompt                                      string
	active, complex, paste, lastCR, unsupported bool
	escape                                      string
	pending                                     []*Submission
}

type Submission struct {
	ID                  string
	Command             string
	Note                string
	candidate, prompt   string
	complex, suppressed bool
	created             time.Time
}

const UnrecognizedInput = "未识别：输入未回显、复杂终端交互或回显不完整；未保存原始按键。"

// Input returns one durable placeholder per Enter (a bracketed paste is one
// submission only when Enter is pressed after the paste). Ctrl-C is cancelled.
func (t *InputTracker) Input(data []byte) ([]*Submission, error) {
	t.expire()
	var out []*Submission
	for _, ch := range string(data) {
		if t.escape != "" && (ch == '\r' || ch == '\n') {
			t.escape = ""
			t.unsupported = true
		}
		if t.escape != "" {
			t.escape += string(ch)
			if len(t.escape) > 64 {
				t.escape = ""
				t.complex = true
				continue
			}
			if t.escape == "\x1b[" || t.escape == "\x1bO" {
				continue
			}
			if len(t.escape) > 2 && ch >= 0x40 && ch <= 0x7e || len(t.escape) == 2 && ch != '[' && ch != 'O' {
				switch t.escape {
				case "\x1b[200~":
					t.paste = true
				case "\x1b[201~":
					t.paste = false
				case "\x1b[D", "\x1bOD":
					t.cursor = max(0, t.cursor-1)
				case "\x1b[C", "\x1bOC":
					t.cursor = min(len(t.line), t.cursor+1)
				case "\x1b[H", "\x1bOH", "\x1b[1~":
					t.cursor = 0
				case "\x1b[F", "\x1bOF", "\x1b[4~":
					t.cursor = len(t.line)
				case "\x1b[3~":
					if t.cursor < len(t.line) {
						t.line = append(t.line[:t.cursor], t.line[t.cursor+1:]...)
					}
				default:
					t.complex = true // history, completion, vi/other editing
				}
				t.escape = ""
			}
			continue
		}
		if ch == '\n' && t.lastCR {
			t.lastCR = false
			continue
		}
		t.lastCR = false
		if !t.active {
			t.prompt = t.screen.text()
			t.active = true
		}
		if ch == '\x1b' {
			t.escape = "\x1b"
			continue
		}
		if (ch == '\r' || ch == '\n') && !t.paste {
			s := &Submission{candidate: string(t.line), prompt: t.prompt, complex: t.complex, suppressed: t.screen.alternate || t.unsupported, created: time.Now(), Note: UnrecognizedInput}
			// Non-empty echoed lines are recorded; blank Enter still leaves evidence.
			out = append(out, s)
			t.pending = append(t.pending, s)
			t.reset()
			t.lastCR = ch == '\r'
			if len(out) > 100 || len(t.pending) > 200 {
				return nil, errors.New("terminal_submission_limit")
			}
			continue
		}
		switch ch {
		case 3:
			t.reset() // Ctrl-C: not submitted
		case 1:
			t.cursor = 0
		case 5:
			t.cursor = len(t.line)
		case 2:
			t.cursor = max(0, t.cursor-1)
		case 6:
			t.cursor = min(len(t.line), t.cursor+1)
		case 8, 127:
			if t.cursor > 0 {
				t.line = append(t.line[:t.cursor-1], t.line[t.cursor:]...)
				t.cursor--
			}
		case 21:
			t.line = t.line[t.cursor:]
			t.cursor = 0
		case 11:
			t.line = t.line[:t.cursor]
		case 23:
			end := t.cursor
			for t.cursor > 0 && unicode.IsSpace(t.line[t.cursor-1]) {
				t.cursor--
			}
			for t.cursor > 0 && !unicode.IsSpace(t.line[t.cursor-1]) {
				t.cursor--
			}
			t.line = append(t.line[:t.cursor], t.line[end:]...)
		case '\r', '\n':
			t.unsupported = true // multiline paste: do not invent a shell parse
		default:
			if ch < 32 {
				t.complex = true
				continue
			}
			if len(t.line) < 4096 {
				t.line = append(t.line, 0)
				copy(t.line[t.cursor+1:], t.line[t.cursor:])
				t.line[t.cursor] = ch
				t.cursor++
			} else {
				t.unsupported = true
			}
		}
	}
	return out, nil
}

func (t *InputTracker) reset() {
	clear(t.line)
	t.line = nil
	t.cursor = 0
	t.prompt = ""
	t.active = false
	t.complex = false
	t.unsupported = false
}

// Output resolves only from the first visible line ending after a submission.
// Unknown/full-screen/multiline input remains an explicit unrecognized record.
func (t *InputTracker) Output(data []byte) []*Submission {
	t.expire()
	var resolved []*Submission
	t.screen.write(data, func(line string) {
		if len(t.pending) == 0 {
			return
		}
		s := t.pending[0]
		t.pending[0] = nil
		t.pending = t.pending[1:]
		defer func() { s.candidate = ""; s.prompt = "" }()
		if s.ID == "" || s.suppressed || t.screen.alternate {
			return
		}
		visible := strings.TrimRight(line, " ")
		if s.prompt != "" && strings.HasPrefix(visible, s.prompt) {
			visible = strings.TrimPrefix(visible, s.prompt)
		}
		if !s.complex {
			// Compare against the locally edited candidate, but save ONLY echoed text.
			if strings.TrimRight(s.candidate, " ") != visible || strings.TrimSpace(visible) == "" {
				return
			}
		} else {
			// History/tab completion is inferred from a redraw with the same prompt.
			if s.prompt == "" || !strings.HasPrefix(line, s.prompt) || strings.TrimSpace(visible) == "" {
				return
			}
		}
		if len(visible) > 4096 || strings.ContainsAny(visible, "\r\n") {
			return
		}
		s.Command = visible
		s.Note = "根据终端回显识别的提交内容；不是远端执行确认，退出码未知。"
		resolved = append(resolved, s)
	})
	return resolved
}

func (t *InputTracker) expire() {
	for len(t.pending) > 0 && time.Since(t.pending[0].created) > 2*time.Second {
		t.pending[0].candidate = ""
		t.pending[0].prompt = ""
		t.pending[0] = nil
		t.pending = t.pending[1:]
	}
}

// A bounded, single-line VT echo decoder. Unsupported cursor movement makes the
// line unrecognizable instead of manufacturing a command from screen content.
type echoLine struct {
	line               []rune
	cursor             int
	escape             string
	utf8               []byte
	alternate, invalid bool
}

func (s *echoLine) text() string {
	if s.invalid || s.alternate {
		return ""
	}
	return string(s.line)
}
func (s *echoLine) write(p []byte, newline func(string)) {
	p = append(s.utf8, p...)
	s.utf8 = nil
	for len(p) > 0 {
		if !utf8.FullRune(p) {
			s.utf8 = append([]byte(nil), p...)
			break
		}
		ch, n := utf8.DecodeRune(p)
		p = p[n:]
		if s.escape != "" {
			s.escape += string(ch)
			if strings.HasPrefix(s.escape, "\x1b]") {
				if ch == 7 || strings.HasSuffix(s.escape, "\x1b\\") || len(s.escape) > 4096 {
					s.escape = ""
				}
				continue
			}
			if len(s.escape) == 2 && (ch == '[' || ch == ']') {
				continue
			}
			if len(s.escape) > 128 {
				s.escape = ""
				s.invalid = true
				continue
			}
			if len(s.escape) == 2 || ch >= 0x40 && ch <= 0x7e {
				seq := s.escape
				s.escape = ""
				if strings.HasPrefix(seq, "\x1b[") {
					s.csi(seq[2:])
				}
				continue
			}
			continue
		}
		switch ch {
		case 27:
			s.escape = "\x1b"
		case '\r':
			s.cursor = 0
		case '\n':
			newline(s.text())
			s.line = nil
			s.cursor = 0
			s.invalid = false
		case 8:
			s.cursor = max(0, s.cursor-1)
		case '\t':
			s.cursor = min(8192, (s.cursor/8+1)*8)
		default:
			if ch < 32 || ch == 127 {
				continue
			}
			if s.cursor >= 8192 {
				s.invalid = true
				continue
			}
			for len(s.line) <= s.cursor {
				s.line = append(s.line, ' ')
			}
			s.line[s.cursor] = ch
			s.cursor++
		}
	}
}
func (s *echoLine) csi(seq string) {
	if len(seq) == 0 {
		return
	}
	op := seq[len(seq)-1]
	arg := seq[:len(seq)-1]
	if arg == "?1049" || arg == "?1047" || arg == "?47" {
		s.alternate = op == 'h'
		s.line = nil
		s.cursor = 0
		s.invalid = false
		return
	}
	n, _ := strconv.Atoi(arg)
	n = max(0, min(n, 8192))
	switch op {
	case 'm', 'h', 'l': // styles and bracketed-paste mode do not change the echo
	case 'K':
		if n == 0 && s.cursor < len(s.line) {
			s.line = s.line[:s.cursor]
		} else if n == 2 {
			s.line = nil
		}
	case 'C':
		s.cursor = min(8192, s.cursor+max(1, n))
	case 'D':
		s.cursor = max(0, s.cursor-max(1, n))
	case 'G':
		s.cursor = max(0, n-1)
	case 'J':
		s.line = nil
		s.cursor = 0
		s.invalid = true
	default:
		s.invalid = true
	}
}
