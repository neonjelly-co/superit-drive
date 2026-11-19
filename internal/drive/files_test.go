package drive

import (
	"testing"

	"github.com/neonjelly-co/superit-drive/internal/index"
)

// Names in the index come from storage listings, which a misbehaving WebDAV server could fill with anything. Only a
// plain name becomes a path, so no name can reach outside its place. (Uploads are refused earlier, by CheckName,
// which the tests through HTTP cover.)
func TestOnlyAPlainNameBecomesAPath(t *testing.T) {
	rekas := index.Place{ID: 1, Kind: index.MyFiles, Owner: "u-reka", Folder: "My files/reka@example.com"}
	ok := map[string]string{
		"Tax notes.pdf":   "My files/reka@example.com/Tax notes.pdf",
		".hidden":         "My files/reka@example.com/.hidden",
		"...":             "My files/reka@example.com/...",
		`back\slash`:      `My files/reka@example.com/back\slash`,
		"line\nbreak.txt": "My files/reka@example.com/line\nbreak.txt", // odd, but a name a storage can hold
	}
	for name, want := range ok {
		if got, ok := pathIn(rekas, name); !ok || got != want {
			t.Errorf("pathIn(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"", ".", "..", "../bence@example.com/Payslip.pdf", "a/b", "/", "x/../../y"} {
		if got, ok := pathIn(rekas, name); ok {
			t.Errorf("pathIn(%q) = %q; want no path", name, got)
		}
	}
}
