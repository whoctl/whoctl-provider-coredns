// Package zonefile reads a DNS master file — the format RFC 1035 §5 defines
// and the one CoreDNS's `file` plugin loads.
//
// # Why this is not a general resolver
//
// It reads what a zone *is*: its origin, its SOA, and one entry per record with
// its owner, type and data. It does not validate rdata against its type, follow
// $INCLUDE, or expand $GENERATE. A zone with those is reported with what was
// read and a note saying so, because a count that is quietly short reads
// exactly like a zone that is quietly short.
//
// The format has three details that are easy to miss and that every real zone
// file relies on: a record with no owner field inherits the previous record's,
// parentheses continue one record across several lines, and the TTL and the
// class are both optional and may appear in either order.
package zonefile

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Zone is a parsed master file.
type Zone struct {
	// Origin is the zone's own name, absolute. It comes from $ORIGIN when the
	// file has one and from the caller otherwise, which is how CoreDNS does it:
	// `file db.example.com example.com` names the origin on the directive.
	Origin string
	// TTL is the $TTL default, in seconds. Zero when the file states none.
	TTL uint32
	// SOA is the zone's start of authority, or nil when it has none — which is
	// a broken zone, and worth being able to report as such.
	SOA *SOA
	// Records is every resource record, in file order.
	Records []Record
	// Notes is what was seen and not expanded.
	Notes []string
}

// Record is one resource record.
type Record struct {
	Name  string // absolute owner name
	TTL   uint32
	Class string // IN, CH, HS
	Type  string // A, AAAA, MX, ...
	Data  []string
	Line  int
}

// SOA is the start of authority's rdata, which is what a zone's freshness is
// read from.
type SOA struct {
	PrimaryNS string
	Mailbox   string
	Serial    uint32
	Refresh   uint32
	Retry     uint32
	Expire    uint32
	Minimum   uint32
}

// classes is the set a bare token may be, so the optional class can be told
// apart from the type without a table of every RR type there is.
var classes = map[string]bool{"IN": true, "CH": true, "HS": true, "CS": true}

// ParseFile reads and parses a zone file. origin is what the Corefile said the
// zone is, and is used unless the file overrides it with $ORIGIN.
func ParseFile(path, origin string) (*Zone, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f, origin)
}

// Parse reads a zone from an open file.
func Parse(r io.Reader, origin string) (*Zone, error) {
	z := &Zone{Origin: absolute(origin, ".")}

	var (
		scanner = bufio.NewScanner(r)
		owner   string
		pending strings.Builder
		depth   int
		start   int
		lineNo  int
	)
	// Zone files hold long TXT records and DNSSEC keys, both of which exceed
	// bufio's default line.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		lineNo++
		text, opened, closed := stripComment(scanner.Text())

		if depth == 0 {
			start = lineNo
			// Leading whitespace is the format's way of saying "same owner as
			// the record before", so it has to survive into the joined line.
			pending.Reset()
		} else {
			pending.WriteString(" ")
		}
		pending.WriteString(text)
		depth += opened - closed
		if depth > 0 {
			continue
		}
		depth = 0

		line := strings.ReplaceAll(strings.ReplaceAll(pending.String(), "(", " "), ")", " ")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "$") {
			z.directive(line, start)
			continue
		}
		rec, ok := z.record(line, start, &owner)
		if ok {
			z.Records = append(z.Records, rec)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return z, nil
}

// directive handles the $ lines.
func (z *Zone) directive(line string, lineNo int) {
	fields := strings.Fields(line)
	switch strings.ToUpper(fields[0]) {
	case "$ORIGIN":
		if len(fields) > 1 {
			z.Origin = absolute(fields[1], z.Origin)
		}
	case "$TTL":
		if len(fields) > 1 {
			z.TTL = duration(fields[1])
		}
	case "$INCLUDE":
		// Following it means reading a file this one names, resolving it
		// against a working directory that belongs to the daemon and not to
		// whoever is reading. What it holds is left out and said so.
		z.Notes = append(z.Notes, fmt.Sprintf("line %d: %s is not followed, so its records are not counted", lineNo, line2(fields)))
	case "$GENERATE":
		z.Notes = append(z.Notes, fmt.Sprintf("line %d: %s is not expanded, so the records it makes are not counted", lineNo, line2(fields)))
	}
}

// record parses one resource record, carrying the owner forward.
func (z *Zone) record(line string, lineNo int, owner *string) (Record, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return Record{}, false
	}

	// A record whose line begins with whitespace has no owner field: it
	// belongs to the one before it. This is the rule that makes an apex block
	// of NS and MX records parse as the apex rather than as a record called
	// "NS".
	if !startsWithSpace(line) {
		*owner = z.name(fields[0])
		fields = fields[1:]
	}
	if *owner == "" {
		*owner = z.Origin
	}

	rec := Record{Name: *owner, Class: "IN", TTL: z.TTL, Line: lineNo}

	// The TTL and the class are both optional and may come in either order, so
	// each is taken while it is recognisable and the first thing that is
	// neither is the type.
	for len(fields) > 0 {
		upper := strings.ToUpper(fields[0])
		if classes[upper] {
			rec.Class = upper
			fields = fields[1:]
			continue
		}
		if ttl, ok := ttlOf(fields[0]); ok {
			rec.TTL = ttl
			fields = fields[1:]
			continue
		}
		break
	}
	if len(fields) == 0 {
		return Record{}, false
	}
	rec.Type = strings.ToUpper(fields[0])
	rec.Data = fields[1:]

	if rec.Type == "SOA" && z.SOA == nil {
		z.SOA = soaOf(rec.Data)
	}
	return rec, true
}

// name makes an owner absolute the way the format does: @ is the origin, a
// trailing dot means it already is, anything else is relative to the origin.
func (z *Zone) name(field string) string {
	if field == "@" {
		return z.Origin
	}
	if strings.HasSuffix(field, ".") {
		return strings.ToLower(field)
	}
	return strings.ToLower(field) + "." + z.Origin
}

// Type counts how many records the zone holds of one type.
func (z *Zone) Type(t string) int {
	n := 0
	for _, r := range z.Records {
		if r.Type == t {
			n++
		}
	}
	return n
}

// Types is the set of record types present, and how many of each.
func (z *Zone) Types() map[string]int {
	out := map[string]int{}
	for _, r := range z.Records {
		out[r.Type]++
	}
	return out
}

// NameServers is the zone's own NS records, which is what a delegation points
// at and the one thing a reader almost always wants.
func (z *Zone) NameServers() []string {
	var out []string
	for _, r := range z.Records {
		if r.Type == "NS" && r.Name == z.Origin && len(r.Data) > 0 {
			out = append(out, r.Data[0])
		}
	}
	return out
}

func soaOf(data []string) *SOA {
	if len(data) < 7 {
		return nil
	}
	return &SOA{
		PrimaryNS: data[0],
		Mailbox:   data[1],
		Serial:    duration(data[2]),
		Refresh:   duration(data[3]),
		Retry:     duration(data[4]),
		Expire:    duration(data[5]),
		Minimum:   duration(data[6]),
	}
}

// stripComment removes a `;` comment and counts the parentheses that survive
// it, since a comment may hold either character and neither one counts.
func stripComment(line string) (text string, opened, closed int) {
	var sb strings.Builder
	inQuote := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line):
			sb.WriteByte(c)
			sb.WriteByte(line[i+1])
			i++
			continue
		case c == '"':
			inQuote = !inQuote
		case c == ';' && !inQuote:
			return sb.String(), opened, closed
		case c == '(' && !inQuote:
			opened++
		case c == ')' && !inQuote:
			closed++
		}
		sb.WriteByte(c)
	}
	return sb.String(), opened, closed
}

// ttlOf reads a TTL, in seconds or in BIND's duration spelling.
func ttlOf(field string) (uint32, bool) {
	if field == "" {
		return 0, false
	}
	if n, err := strconv.ParseUint(field, 10, 32); err == nil {
		return uint32(n), true
	}
	// "1h", "2w30m": digits and unit letters and nothing else. A type like
	// "MX" has no digits and a name like "www1" is never in this position.
	var total, current uint64
	seenDigit, seenUnit := false, false
	for i := 0; i < len(field); i++ {
		c := field[i]
		switch {
		case c >= '0' && c <= '9':
			current = current*10 + uint64(c-'0')
			seenDigit = true
		case !seenDigit:
			return 0, false
		default:
			mult, ok := units[c|0x20] // lowercase
			if !ok {
				return 0, false
			}
			total += current * mult
			current, seenDigit, seenUnit = 0, false, true
		}
	}
	if !seenUnit || seenDigit {
		return 0, false // trailing digits with no unit, or no unit at all
	}
	return uint32(total), true
}

var units = map[byte]uint64{'s': 1, 'm': 60, 'h': 3600, 'd': 86400, 'w': 604800}

// duration is ttlOf for a field that must be a number, defaulting to zero.
func duration(field string) uint32 {
	if n, ok := ttlOf(field); ok {
		return n
	}
	return 0
}

func absolute(name, fallback string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return fallback
	}
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

func startsWithSpace(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}

func line2(fields []string) string { return strings.Join(fields, " ") }
