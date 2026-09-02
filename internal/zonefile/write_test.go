package zonefile

import (
	"os"
	"strings"
	"testing"
)

func zoneFile(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile("../../testdata/etc/coredns/db.example.com")
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

// The promise of a marked region: every line outside it comes back identical.
// Not reformatted, not reordered, not reparsed — including the lines this
// package would not know what to do with.
func TestEverythingOutsideTheRegionSurvivesByteForByte(t *testing.T) {
	src := zoneFile(t)
	out := SetRegion(src, DefaultRegion, "host1\tIN\tA\t192.0.2.10\n")

	if !strings.HasPrefix(out, src[:strings.LastIndex(src, "\n")]) {
		t.Fatal("the original text was modified before the region was added")
	}
	for _, line := range strings.Split(src, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.Contains(out, line) {
			t.Errorf("line lost or rewritten: %q", line)
		}
	}
	// The hand-aligned SOA, its per-field comments and the quoted TXT are the
	// three things a rewrite would have flattened.
	for _, keep := range []string{
		"        2026080501 ; serial",
		`_dmarc  IN  TXT     "v=DMARC1; p=none; rua=mailto:dmarc@example.com"`,
		"api     600 IN  A   192.0.2.80",
	} {
		if !strings.Contains(out, keep) {
			t.Errorf("not preserved: %q", keep)
		}
	}
}

func TestARegionRoundTrips(t *testing.T) {
	body := "host1\tIN\tA\t192.0.2.10\nhost2\tIN\tA\t192.0.2.11"
	out := SetRegion(zoneFile(t), DefaultRegion, body)

	got, found := Region(out, DefaultRegion)
	if !found {
		t.Fatal("the region was written and cannot be read back")
	}
	if got != body {
		t.Errorf("region = %q, want %q", got, body)
	}
}

// Writing the same body twice must not grow the file: a second pass of an
// adapter is the ordinary case, not the exception.
func TestWritingTwiceIsWritingOnce(t *testing.T) {
	once := SetRegion(zoneFile(t), DefaultRegion, "host1\tIN\tA\t192.0.2.10")
	twice := SetRegion(once, DefaultRegion, "host1\tIN\tA\t192.0.2.10")
	if once != twice {
		t.Error("the second write changed the file")
	}
}

// Two writers, two regions, and neither reaches the other's records. This is
// what lets one adapter prune without taking another's records with it.
func TestRegionsDoNotReachEachOther(t *testing.T) {
	src := SetRegion(zoneFile(t), "leases", "a\tIN\tA\t192.0.2.1")
	src = SetRegion(src, "manual", "b\tIN\tA\t192.0.2.2")

	src = SetRegion(src, "leases", "")
	if body, _ := Region(src, "manual"); body != "b\tIN\tA\t192.0.2.2" {
		t.Errorf("emptying one region changed the other: %q", body)
	}
	if body, found := Region(src, "leases"); !found || body != "" {
		t.Errorf("leases = %q, %v; want an empty region that is still there", body, found)
	}
}

// An emptied region keeps its markers. The file then says "whoctl writes here
// and currently has nothing to say", which is a different fact from a file
// whoctl has never touched.
func TestAnEmptiedRegionKeepsItsMarkers(t *testing.T) {
	src := SetRegion(zoneFile(t), DefaultRegion, "a\tIN\tA\t192.0.2.1")
	src = SetRegion(src, DefaultRegion, "")
	if !strings.Contains(src, Begin(DefaultRegion)) || !strings.Contains(src, End(DefaultRegion)) {
		t.Error("the markers were removed with the records")
	}
}

// A file somebody edited half way through, leaving an opening marker and no
// close. Guessing where the region ended would be writing over whatever came
// after it.
func TestAnUnclosedRegionIsNotARegion(t *testing.T) {
	src := zoneFile(t) + "\n" + Begin(DefaultRegion) + "\nhost1\tIN\tA\t192.0.2.10\n"
	if _, found := Region(src, DefaultRegion); found {
		t.Error("an unclosed region was read as one")
	}
	// And a write appends a whole new region rather than closing that one.
	out := SetRegion(src, DefaultRegion, "host2\tIN\tA\t192.0.2.11")
	if strings.Count(out, Begin(DefaultRegion)) != 2 {
		t.Errorf("want the damaged region left alone and a new one appended:\n%s", out)
	}
}

// The serial is where a zone file is at its most human: several lines, a
// comment per field, aligned by hand. Only the number may change.
func TestBumpSerialTouchesNothingButTheNumber(t *testing.T) {
	src := zoneFile(t)
	out, serial, err := BumpSerial(src)
	if err != nil {
		t.Fatal(err)
	}
	if serial != 2026080502 {
		t.Errorf("serial = %d, want 2026080502", serial)
	}
	if !strings.Contains(out, "        2026080502 ; serial") {
		t.Error("the alignment or the comment did not survive the bump")
	}
	if strings.Count(out, "\n") != strings.Count(src, "\n") {
		t.Error("the bump changed how many lines the file has")
	}
	// Every other SOA field is a number too, and the refresh sits one line
	// below the serial: an off-by-one here is silent and permanent.
	for _, keep := range []string{"7200       ; refresh", "3600       ; retry", "1209600    ; expire"} {
		if !strings.Contains(out, keep) {
			t.Errorf("a different SOA field was changed: %q is gone", keep)
		}
	}
}

func TestBumpSerialIsMonotonic(t *testing.T) {
	src := zoneFile(t)
	for want := uint32(2026080502); want < 2026080505; want++ {
		out, got, err := BumpSerial(src)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("serial = %d, want %d", got, want)
		}
		src = out
	}
}

// A zone with no SOA is not a zone CoreDNS answers for, and writing records
// into it without saying so would be reporting success for something nobody
// will ever be served.
func TestBumpSerialRefusesAZoneWithNoSOA(t *testing.T) {
	if _, _, err := BumpSerial("$ORIGIN example.com.\nwww IN A 192.0.2.1\n"); err == nil {
		t.Error("no error for a zone with no SOA")
	}
}

// A single-line SOA is legal and common in generated zones, and the token
// walker has to find the serial there too.
func TestBumpSerialOnASingleLineSOA(t *testing.T) {
	src := "@ IN SOA ns1.example.com. hostmaster.example.com. 12 7200 3600 1209600 3600\n"
	out, serial, err := BumpSerial(src)
	if err != nil {
		t.Fatal(err)
	}
	if serial != 13 || !strings.Contains(out, "hostmaster.example.com. 13 7200") {
		t.Errorf("out = %q, serial = %d", out, serial)
	}
}
