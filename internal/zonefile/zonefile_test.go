package zonefile

import (
	"strings"
	"testing"
)

const fixture = `$ORIGIN example.com.
$TTL 3600
@   IN  SOA ns1.example.com. hostmaster.example.com. (
        2026080501 ; serial
        7200       ; refresh
        3600       ; retry
        1209600    ; expire
        3600 )     ; minimum

    IN  NS      ns1.example.com.
    IN  NS      ns2.example.com.
    IN  MX  10  mail.example.com.

ns1     IN  A       192.0.2.53
www     IN  CNAME   example.com.
api     600 IN  A   192.0.2.80
short   IN  600 A   192.0.2.81
_dmarc  IN  TXT     "v=DMARC1; p=none; rua=mailto:dmarc@example.com"
`

func parse(t *testing.T, src, origin string) *Zone {
	t.Helper()
	z, err := Parse(strings.NewReader(src), origin)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return z
}

// Parentheses continue one record across several lines. Without this the SOA is
// six broken records and the zone has no serial.
func TestParenthesesContinueARecord(t *testing.T) {
	z := parse(t, fixture, "")
	if z.SOA == nil {
		t.Fatal("no SOA")
	}
	want := SOA{
		PrimaryNS: "ns1.example.com.", Mailbox: "hostmaster.example.com.",
		Serial: 2026080501, Refresh: 7200, Retry: 3600, Expire: 1209600, Minimum: 3600,
	}
	if *z.SOA != want {
		t.Errorf("SOA = %+v, want %+v", *z.SOA, want)
	}
	if z.Type("SOA") != 1 {
		t.Errorf("SOA records = %d, want one", z.Type("SOA"))
	}
}

// A record whose line begins with whitespace belongs to the owner before it.
// Without this the apex NS records are owned by something called "NS".
func TestAnOwnerlessRecordInheritsTheOneBefore(t *testing.T) {
	z := parse(t, fixture, "")
	ns := z.NameServers()
	if len(ns) != 2 || ns[0] != "ns1.example.com." || ns[1] != "ns2.example.com." {
		t.Errorf("nameServers = %v, want both, owned by the apex", ns)
	}
	for _, r := range z.Records {
		if r.Type == "MX" && r.Name != "example.com." {
			t.Errorf("the MX is owned by %q, want the apex", r.Name)
		}
	}
}

// The TTL and the class are both optional and may come in either order, and a
// parser that assumes one order reads the TTL as the type.
func TestTTLAndClassInEitherOrder(t *testing.T) {
	z := parse(t, fixture, "")
	byName := map[string]Record{}
	for _, r := range z.Records {
		byName[r.Name] = r
	}
	for name, want := range map[string]uint32{
		"api.example.com.":   600,
		"short.example.com.": 600,
		"ns1.example.com.":   3600, // no TTL of its own: the $TTL default
	} {
		got := byName[name]
		if got.TTL != want || got.Type != "A" {
			t.Errorf("%s = {type %s ttl %d}, want {A %d}", name, got.Type, got.TTL, want)
		}
	}
}

// A semicolon inside a quoted string is data, not a comment. A DMARC record is
// the case that proves it, and truncating one is silent.
func TestASemicolonInsideQuotesIsNotAComment(t *testing.T) {
	z := parse(t, fixture, "")
	for _, r := range z.Records {
		if r.Type != "TXT" {
			continue
		}
		if data := strings.Join(r.Data, " "); !strings.Contains(data, "rua=mailto:dmarc@example.com") {
			t.Errorf("TXT = %q, want the whole value", data)
		}
		return
	}
	t.Error("the TXT record was lost entirely")
}

// Names are made absolute against the origin, @ is the origin, and a trailing
// dot means it already is.
func TestNamesAreMadeAbsolute(t *testing.T) {
	z := parse(t, fixture, "")
	names := map[string]bool{}
	for _, r := range z.Records {
		names[r.Name] = true
	}
	for _, want := range []string{"example.com.", "ns1.example.com.", "_dmarc.example.com."} {
		if !names[want] {
			t.Errorf("%q is missing from %v", want, names)
		}
	}
}

// A file with no $ORIGIN takes the one the Corefile's file directive names,
// which is the order CoreDNS resolves it in.
func TestTheOriginFallsBackToTheCorefiles(t *testing.T) {
	z := parse(t, "$TTL 1h\n@ IN SOA ns. admin. ( 1 2 3 4 5 )\nweb IN A 10.0.0.1\n", "internal.test.")
	if z.Origin != "internal.test." {
		t.Fatalf("origin = %q", z.Origin)
	}
	if z.Records[1].Name != "web.internal.test." {
		t.Errorf("name = %q", z.Records[1].Name)
	}
	if z.TTL != 3600 {
		t.Errorf("$TTL 1h = %d, want 3600", z.TTL)
	}
}

func TestDurations(t *testing.T) {
	for field, want := range map[string]uint32{
		"3600": 3600, "1h": 3600, "15m": 900, "2w": 1209600, "1d": 86400,
		"1h30m": 5400, "0": 0,
	} {
		got, ok := ttlOf(field)
		if !ok || got != want {
			t.Errorf("ttlOf(%q) = %d, %v; want %d, true", field, got, ok, want)
		}
	}
	// A type is not a TTL, or the type is eaten and the rdata becomes the type.
	for _, field := range []string{"A", "AAAA", "MX", "CNAME", "TXT", "NS", "SOA", "1h2", ""} {
		if _, ok := ttlOf(field); ok {
			t.Errorf("ttlOf(%q) read a TTL out of something that is not one", field)
		}
	}
}

// A count that is quietly short reads exactly like a zone that is quietly
// short, so what was not expanded is said out loud.
func TestWhatIsNotExpandedIsSaidOutLoud(t *testing.T) {
	z := parse(t, "$ORIGIN example.com.\n$INCLUDE db.more\n$GENERATE 1-10 host$ IN A 10.0.0.$\n", "")
	if len(z.Notes) != 2 {
		t.Fatalf("notes = %v, want one per unexpanded directive", z.Notes)
	}
	if !strings.Contains(z.Notes[0], "$INCLUDE") || !strings.Contains(z.Notes[1], "$GENERATE") {
		t.Errorf("notes = %v", z.Notes)
	}
}

func TestTypes(t *testing.T) {
	z := parse(t, fixture, "")
	want := map[string]int{"SOA": 1, "NS": 2, "MX": 1, "A": 3, "CNAME": 1, "TXT": 1}
	got := z.Types()
	if len(got) != len(want) {
		t.Fatalf("types = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d", k, got[k], v)
		}
	}
	if len(z.Records) != 9 {
		t.Errorf("records = %d, want 9", len(z.Records))
	}
}
