package text

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTheLanguageIsTheOneTheBrowserPrefers(t *testing.T) {
	for _, c := range []struct {
		header string
		want   Lang
	}{
		{"", English},
		{"hu", Hungarian},
		{"hu-HU,hu;q=0.9,en-US;q=0.8,en;q=0.7", Hungarian},
		{"en-GB,en;q=0.9,hu;q=0.8", English},
		{"de-DE,de;q=0.9,hu;q=0.8,en;q=0.5", Hungarian},
		{"de-DE,de;q=0.9", English},
		{"*", English},
		{"en;q=0.5, hu;q=0.6", Hungarian},
		{"hu;q=0.5, en;q=0.5", Hungarian}, // the same weight: the first named
		{"en;q=0.5, hu;q=0.5", English},
		{"hu;q=0, en;q=0.1", English}, // q=0 says "not this one"
		{"HU-hu", Hungarian},
		{"hungarian, en", English},
	} {
		if got := FromHeader(c.header); got != c.want {
			t.Errorf("Accept-Language %q: got %s, want %s", c.header, got.Code(), c.want.Code())
		}
	}
}

func TestALanguageChosenInTheAddressComesFirst(t *testing.T) {
	for _, c := range []struct {
		target, header string
		want           Lang
	}{
		{"/s/x?lang=hu", "en", Hungarian},
		{"/s/x?lang=en", "hu", English},
		{"/s/x?lang=de", "hu", Hungarian},
		{"/s/x?lang=", "hu", Hungarian},
		{"/s/x", "hu", Hungarian},
		{"/s/x?language=en", "hu", Hungarian},
	} {
		r := httptest.NewRequest("GET", c.target, nil)
		r.Header.Set("Accept-Language", c.header)
		if got := Of(r); got != c.want {
			t.Errorf("%s with Accept-Language %q: got %s, want %s", c.target, c.header, got.Code(), c.want.Code())
		}
	}
}

func TestAMessageSaysItselfInEachLanguage(t *testing.T) {
	m := M("“%s” was moved to the trash.", "Kukába került: „%s”.")
	if got := m.In(English, "Notes.txt"); got != "“Notes.txt” was moved to the trash." {
		t.Errorf("English: %q", got)
	}
	if got := m.In(Hungarian, "Notes.txt"); got != "Kukába került: „Notes.txt”." {
		t.Errorf("Hungarian: %q", got)
	}
	if p := m.Problem(); p != "" {
		t.Errorf("a whole message has a problem: %s", p)
	}
	plain := M("Try again.", "Próbáld újra.")
	if got := plain.In(Hungarian); got != "Próbáld újra." {
		t.Errorf("without values: %q", got)
	}
}

func TestAMessageWithAMissingOrUntranslatedLanguageIsFound(t *testing.T) {
	for _, c := range []struct {
		m    Message
		want string
	}{
		{Message{[2]string{"Try again.", ""}}, "no Hungarian"},
		{Message{[2]string{"Try again.", "Try again."}}, "untranslated"},
		{Message{[2]string{"“%s” is here.", "Itt van."}}, "do not say the same values"},
		{Message{[2]string{"%s of %d", "%[2]d és %[1]s"}}, ""},
		{Message{[2]string{"%s of %d", "%[2]s és %[1]d"}}, "do not say the same values"},
		{Message{[2]string{"100%% done", "100%%-ban kész"}}, ""},
	} {
		got := c.m.Problem()
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%v: got problem %q, want one saying %q", c.m.in, got, c.want)
		}
	}
}

func TestSizesAndMomentsReadAsEachLanguageWritesThem(t *testing.T) {
	for _, c := range []struct {
		bytes  int64
		en, hu string
	}{
		{999, "999 B", "999 B"},
		{1200, "1.2 KB", "1,2 KB"},
		{84_000, "84 KB", "84 KB"},
		{2_500_000_000, "2.5 GB", "2,5 GB"},
	} {
		if got := Size(English, c.bytes); got != c.en {
			t.Errorf("%d bytes in English: %q", c.bytes, got)
		}
		if got := Size(Hungarian, c.bytes); got != c.hu {
			t.Errorf("%d bytes in Hungarian: %q", c.bytes, got)
		}
	}
	at := time.Date(2026, time.December, 3, 14, 5, 0, 0, time.FixedZone("CET", 3600))
	if got := Moment(English, at); got != "3 December 2026, 13:05 UTC" {
		t.Errorf("English: %q", got)
	}
	if got := Moment(Hungarian, at); got != "2026. december 3. 13:05 (UTC)" {
		t.Errorf("Hungarian: %q", got)
	}
}
