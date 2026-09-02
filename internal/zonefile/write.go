package zonefile

import (
	"fmt"
	"strconv"
	"strings"
)

// The region markers.
//
// # Why a marked region rather than rewriting the file
//
// The objection this package's parser was written under is that a rewrite has
// to give back every directive, comment and record it did not model, and a zone
// file is full of things nobody here has modelled — DNSSEC records, $GENERATE,
// the blank line somebody left between two groups on purpose.
//
// A marked region answers it without solving it. What is between the markers is
// whoctl's, and it is rewritten whole; every other line of the file is copied
// back byte for byte, including the ones this package could not parse. The
// ownership is then legible in the file itself, which is what makes deleting
// safe: whoctl removes what it wrote and cannot reach anything else.
//
// It is the same technique the documentation generator uses on markdown, where
// the markers are HTML comments. Here they are zone file comments, so CoreDNS
// reads straight past them.
const (
	markerPrefix = "; whoctl:begin "
	markerSuffix = "; whoctl:end "
)

// DefaultRegion is the region a record belongs to when nothing says otherwise.
// A second writer — an adapter keeping its own records in step — names its own,
// so that pruning one never reaches the other's.
const DefaultRegion = "whoctl"

// Begin and End are the marker lines for a region.
func Begin(region string) string { return markerPrefix + region }
func End(region string) string   { return markerSuffix + region }

// Region returns the lines between a region's markers.
//
// found is false when the file has no such region, which is different from a
// region that is there and empty: the first is a file whoctl has never written
// to, and the second is one it has emptied.
func Region(src, region string) (body string, found bool) {
	lines := strings.Split(src, "\n")
	start, end := bounds(lines, region)
	if start < 0 || end < 0 {
		return "", false
	}
	return strings.Join(lines[start+1:end], "\n"), true
}

// SetRegion writes body between a region's markers, adding the region at the
// end of the file when it is not there yet.
//
// Everything outside the markers is untouched — not reformatted, not reordered,
// not reparsed. That is the whole promise of this function.
func SetRegion(src, region, body string) string {
	lines := strings.Split(src, "\n")
	inner := []string{}
	if strings.TrimSpace(body) != "" {
		inner = strings.Split(strings.TrimRight(body, "\n"), "\n")
	}

	start, end := bounds(lines, region)
	if start >= 0 && end >= 0 {
		out := append([]string{}, lines[:start+1]...)
		out = append(out, inner...)
		out = append(out, lines[end:]...)
		return strings.Join(out, "\n")
	}

	// A new region goes at the end, after a blank line, and the file keeps its
	// trailing newline. Appending is deliberate: inserting it anywhere else
	// would mean deciding where, and every answer to that moves lines somebody
	// else wrote.
	trimmed := strings.TrimRight(src, "\n")
	block := append([]string{"", Begin(region)}, inner...)
	block = append(block, End(region), "")
	return trimmed + "\n" + strings.Join(block, "\n")
}

// bounds finds the marker lines of a region, as indices into lines.
func bounds(lines []string, region string) (start, end int) {
	start, end = -1, -1
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case Begin(region):
			if start < 0 {
				start = i
			}
		case End(region):
			if start >= 0 && end < 0 {
				end = i
			}
		}
	}
	if end < 0 {
		// An opened region with no end is a file somebody edited half way
		// through, and guessing where it should have ended would be writing
		// over whatever came after it.
		return -1, -1
	}
	return start, end
}

// BumpSerial increments the zone's SOA serial in place, returning the new text
// and the new value.
//
// # Why this is textual and not a rewrite of the record
//
// Because the SOA is where a zone file is at its most human: it spans lines,
// carries a comment per field, and is aligned by hand. Reformatting it to
// change one number would be the thing marked regions exist to avoid — so this
// finds the serial where it is and replaces exactly that token.
//
// # Why it must happen at all
//
// A secondary decides whether to transfer by comparing serials, and CoreDNS
// decides whether a reload changed anything the same way. Records written
// without a bump are records that reach nobody but this machine, and nothing
// reports that: the file is right and the world does not know.
func BumpSerial(src string) (string, uint32, error) {
	z, err := Parse(strings.NewReader(src), ".")
	if err != nil {
		return "", 0, err
	}
	if z.SOA == nil {
		return "", 0, fmt.Errorf("the zone has no SOA record, so there is no serial to bump")
	}

	var soaLine int
	for _, rec := range z.Records {
		if strings.EqualFold(rec.Type, "SOA") {
			soaLine = rec.Line
			break
		}
	}
	if soaLine == 0 {
		return "", 0, fmt.Errorf("the SOA record was parsed but not located in the file")
	}

	lines := strings.Split(src, "\n")
	tokens := scanTokens(lines, soaLine-1)

	// primary, mailbox, serial — the serial is the third field of the rdata,
	// which begins after the type.
	at := -1
	for i, tok := range tokens {
		if strings.EqualFold(tok.text, "SOA") {
			at = i + 3
			break
		}
	}
	if at < 0 || at >= len(tokens) {
		return "", 0, fmt.Errorf("the SOA record does not have a serial where one has to be")
	}

	tok := tokens[at]
	current, err := strconv.ParseUint(tok.text, 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("the SOA serial is %q, which is not a number", tok.text)
	}
	// Plus one, and nothing cleverer.
	//
	// The only requirement is that it goes up. Rewriting it as today's date
	// with a counter — which is what the convention YYYYMMDDNN means — would
	// impose a convention on a file that may not be using it, and would run out
	// after a hundred changes in a day. A zone already numbered that way still
	// reads correctly: 2026080501 becomes 2026080502.
	next := uint32(current + 1)

	line := lines[tok.line]
	lines[tok.line] = line[:tok.col] + strconv.FormatUint(uint64(next), 10) + line[tok.col+len(tok.text):]
	return strings.Join(lines, "\n"), next, nil
}

// token is one field of a record, and where it is in the file.
type token struct {
	text string
	line int // index into lines
	col  int
}

// scanTokens reads the record beginning at a line, following parentheses onto
// the lines that continue it.
//
// It is a second, smaller scanner than the parser's on purpose: that one joins
// continuation lines into one string and loses every position in the process,
// and a position is the whole of what this needs.
func scanTokens(lines []string, start int) []token {
	var out []token
	depth := 0

	for i := start; i < len(lines); i++ {
		line := lines[i]
		for j := 0; j < len(line); {
			switch c := line[j]; {
			case c == ';':
				// A comment runs to the end of the line, wherever it starts.
				j = len(line)
			case c == '(':
				depth++
				j++
			case c == ')':
				depth--
				j++
			case c == ' ' || c == '\t' || c == '\r':
				j++
			case c == '"':
				k := j + 1
				for k < len(line) && line[k] != '"' {
					if line[k] == '\\' {
						k++
					}
					k++
				}
				if k < len(line) {
					k++
				}
				out = append(out, token{line[j:k], i, j})
				j = k
			default:
				k := j
				for k < len(line) && !isFieldEnd(line[k]) {
					k++
				}
				out = append(out, token{line[j:k], i, j})
				j = k
			}
		}
		if depth <= 0 {
			return out
		}
	}
	return out
}

func isFieldEnd(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == ';' || c == '(' || c == ')'
}

// RegionBounds reports the line numbers of a region's markers, one-based, as a
// record's Line is.
//
// It is what tells a record inside whoctl's region from one somebody wrote by
// hand, which is the difference between a record whoctl may change and one it
// must refuse to touch.
func RegionBounds(src, region string) (start, end int, ok bool) {
	lines := strings.Split(src, "\n")
	s, e := bounds(lines, region)
	if s < 0 || e < 0 {
		return 0, 0, false
	}
	return s + 1, e + 1, true
}
