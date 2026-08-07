package corefile

import (
	"os"
	"regexp"
	"strings"
)

// token is one whitespace-separated word of the source, with the line it came
// from. The line matters: a Caddyfile directive ends at the end of its line,
// so without it there is no way to tell where one plugin's arguments stop and
// the next plugin begins.
type token struct {
	text   string
	line   int
	quoted bool
}

// lex splits the source into tokens the way Caddyfile does.
//
// Two rules are not obvious and both come from Caddy's own lexer, which is what
// CoreDNS runs: a `#` outside quotes begins a comment wherever it appears, not
// only at the start of a word, and a quoted string may contain anything
// including whitespace and `#`.
func lex(src string) []token {
	var toks []token
	line, i, n := 1, 0, len(src)
	for i < n {
		switch c := src[i]; {
		case c == '\n':
			line++
			i++
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '#':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '"':
			start := line
			i++
			var sb strings.Builder
			for i < n {
				if src[i] == '\\' && i+1 < n {
					sb.WriteByte(src[i+1])
					i += 2
					continue
				}
				if src[i] == '"' {
					i++
					break
				}
				if src[i] == '\n' {
					line++
				}
				sb.WriteByte(src[i])
				i++
			}
			toks = append(toks, token{text: expandEnv(sb.String()), line: start, quoted: true})
		default:
			start := i
			for i < n && !isSpace(src[i]) && src[i] != '#' {
				i++
			}
			toks = append(toks, token{text: expandEnv(src[start:i]), line: line})
		}
	}
	return toks
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// envPattern is CoreDNS's {$VAR}. It is substituted here because CoreDNS
// substitutes it: a Corefile listening on `{$PORT}` is listening on a number,
// and reporting the literal would be reporting something that is not true.
//
// The cost is that what this provider reads depends on the environment, which
// is the same thing the aws provider's credential chain does and for the same
// reason — the provider behaves as whoever started it, and a server's context
// sets that environment deliberately.
var envPattern = regexp.MustCompile(`\{\$([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnv(s string) string {
	if !strings.Contains(s, "{$") {
		return s
	}
	return envPattern.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[2 : len(m)-1])
	})
}

// stmt is one statement: the words before a block, and the block's tokens.
//
// A server block and a plugin line are the same shape — a head, and optionally
// a body — which is why one function parses both and why a plugin's own block
// nests without any extra machinery.
type stmt struct {
	head      []token
	body      []token
	line      int
	openLine  int
	closeLine int
	hasBlock  bool
}

// statements splits a token stream into statements.
func statements(toks []token) []stmt {
	var out []stmt
	i := 0
	for i < len(toks) {
		if toks[i].text == "}" {
			i++ // a close with nothing open; the caller's block already ended
			continue
		}
		st := stmt{line: toks[i].line}
		lineNo := toks[i].line
		for i < len(toks) && toks[i].line == lineNo && toks[i].text != "{" && toks[i].text != "}" {
			st.head = append(st.head, toks[i])
			i++
		}

		// Caddyfile requires the brace on the same line as the head, and a
		// Corefile that puts it on the next one is still unambiguous: a lone
		// `{` is never a plugin name. Accepting it costs nothing and turns a
		// misparse into a parse.
		if i < len(toks) && toks[i].text == "{" {
			st.hasBlock = true
			st.openLine = toks[i].line
			i++
			depth, start := 1, i
			for i < len(toks) {
				if toks[i].text == "{" {
					depth++
				}
				if toks[i].text == "}" {
					if depth--; depth == 0 {
						break
					}
				}
				i++
			}
			st.body = toks[start:min(i, len(toks))]
			if i < len(toks) {
				st.closeLine = toks[i].line
				i++
			}
		}

		if len(st.head) > 0 || st.hasBlock {
			out = append(out, st)
		}
	}
	return out
}

// directives parses a statement's body one level down, and recurses, so a
// plugin's own block is read the same way the server block was.
func (st stmt) directives(lines []string) []Directive {
	var out []Directive
	for _, s := range statements(st.body) {
		if len(s.head) == 0 {
			continue
		}
		d := Directive{Name: s.head[0].text, Line: s.line}
		for _, t := range s.head[1:] {
			d.Args = append(d.Args, t.text)
		}
		if s.hasBlock {
			d.Body = s.bodyLines(lines)
			d.Blocks = s.directives(lines)
		}
		out = append(out, d)
	}
	return out
}

// bodyLines is the raw source between the braces, which is what a future
// rewrite gives back for everything it did not model.
func (st stmt) bodyLines(lines []string) []string {
	if !st.hasBlock {
		return nil
	}
	// openLine and closeLine are 1-based, so the line after the brace is at
	// index openLine and the line before the closing one ends at closeLine-1.
	start, end := st.openLine, st.closeLine-1
	if st.closeLine == 0 { // unclosed: everything that is left belongs to it
		end = len(lines)
	}
	start, end = max(start, 0), min(end, len(lines))
	if start >= end {
		return nil
	}
	return append([]string(nil), lines[start:end]...)
}
