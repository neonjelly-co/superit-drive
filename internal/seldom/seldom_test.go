package seldom

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// A line anyone can cause is written once in every interval, however often it is asked for, and the next one says how
// many were left out.
func TestALineIsWrittenOnceInEveryInterval(t *testing.T) {
	var out bytes.Buffer
	log := slog.New(slog.NewTextHandler(&out, nil))
	now := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	l := Every(time.Minute)
	l.now = func() time.Time { return now }

	for range 1000 {
		l.Log(log, slog.LevelWarn, "refused", "path", "/x")
		now = now.Add(10 * time.Millisecond)
	}
	if n := strings.Count(out.String(), "refused"); n != 1 {
		t.Fatalf("1000 asks within ten seconds wrote %d lines, want 1:\n%s", n, out.String())
	}
	if strings.Contains(out.String(), "leftOut") {
		t.Errorf("the first line says some were left out: %s", out.String())
	}

	out.Reset()
	now = now.Add(time.Minute)
	l.Log(log, slog.LevelWarn, "refused", "path", "/y")
	if got := out.String(); !strings.Contains(got, "path=/y") || !strings.Contains(got, "leftOut=999") {
		t.Errorf("the line after the interval: %q, want it written, with the 999 left out", got)
	}

	out.Reset()
	now = now.Add(time.Minute)
	l.Log(log, slog.LevelWarn, "refused", "path", "/z")
	if got := out.String(); !strings.Contains(got, "path=/z") || strings.Contains(got, "leftOut") {
		t.Errorf("a line with none left out before it: %q", got)
	}
}

// What a line quotes of what the person sent is cut short, and never in the middle of a character.
func TestWhatALineQuotesIsCutShort(t *testing.T) {
	if got := Cut("/api/places/1"); got != "/api/places/1" {
		t.Errorf("a short path was changed: %q", got)
	}
	long := Cut(strings.Repeat("a", 1<<20))
	if len(long) > Most+len("…") || !strings.HasSuffix(long, "…") {
		t.Errorf("a megabyte was cut to %d bytes, want at most %d ending in …", len(long), Most+len("…"))
	}
	accented := Cut(strings.Repeat("ő", Most)) // two bytes each: the cut falls between two of them, or is moved there
	if !utf8.ValidString(accented) || len(accented) > Most+len("…") {
		t.Errorf("cut in the middle of a character, or too long: %d bytes, valid %v", len(accented), utf8.ValidString(accented))
	}
	odd := Cut("x" + strings.Repeat("ő", Most))
	if !utf8.ValidString(odd) || len(odd) > Most+len("…") {
		t.Errorf("cut in the middle of a character, or too long: %d bytes, valid %v", len(odd), utf8.ValidString(odd))
	}
	if got := Cut(strings.Repeat("\xff", 2*Most)); !utf8.ValidString(got) {
		t.Errorf("bytes that are no text were quoted as they came")
	}
}
