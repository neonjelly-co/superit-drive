package server

import (
	"strings"
	"testing"
)

// What opens in the browser, written out here rather than taken from the list: every extension that opens, with the
// content type sent for it. Nothing else opens.
var opensAs = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp",
	".avif": "image/avif", ".bmp": "image/bmp",
	".pdf": "application/pdf",
	".txt": "text/plain; charset=utf-8", ".log": "text/plain; charset=utf-8", ".md": "text/plain; charset=utf-8",
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm",
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".wav": "audio/wav", ".flac": "audio/flac",
	".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg",
}

func TestOnlyTheListOpensInTheBrowser(t *testing.T) {
	if len(opensInBrowser) != len(opensAs) {
		t.Errorf("the list has %d kinds, the test %d", len(opensInBrowser), len(opensAs))
	}
	for ext, want := range opensAs {
		for _, name := range []string{"File" + ext, "File" + strings.ToUpper(ext), "a.b.c" + ext, "Árvíztűrő tükörfúrógép" + ext} {
			o, ok := openingOf(name)
			if !ok || o.contentType != want {
				t.Errorf("%q: opens %v as %q, want %q", name, ok, o.contentType, want)
			}
			// Only what plays (a video, a sound) keeps its origin, so the player can fetch it again; nothing may run.
			media := strings.HasPrefix(want, "video/") || strings.HasPrefix(want, "audio/")
			if (media && o.policy != playable) || (!media && o.policy != sandboxed) {
				t.Errorf("%q: policy %q", name, o.policy)
			}
		}
	}
	for _, name := range []string{
		"Page.html", "Page.htm", "Page.xhtml", "Page.shtml", "Drawing.svg", "Drawing.svgz", "Data.xml", "Style.xsl",
		"Script.js", "Module.mjs", "Applet.jar", "Flash.swf", "Page.mht", "Page.mhtml", "Feed.rss", "Feed.atom",
		"Style.css", "Data.json", "Table.csv", "Scan.tif", "Scan.tiff", "Icon.ico", "Photo.heic", "Clip.mov",
		"Clip.ogv", "Clip.avi", "Clip.mkv", "Letter.docx", "Offer.pdf.html", "Photo.png.svg", ".png", "png", "Photo",
		"Photo.", "Photo.png.", "Photo.png ", "Photo.\uff50\uff4e\uff47", "Photo.pn\u0261", "Photo.p\u200bng", "Photo.pn g",
		"Photo.png\u0301", "Photo.\u212aey", "",
	} {
		if o, ok := openingOf(name); ok {
			t.Errorf("%q opens as %q; it must download", name, o.contentType)
		}
	}
	for _, o := range opensInBrowser {
		if !strings.Contains(o.policy, "default-src 'none'") || !strings.Contains(o.policy, "sandbox") ||
			strings.Contains(o.policy, "allow-scripts") || strings.Contains(o.policy, "script-src") {
			t.Errorf("%q is opened under %q, which must let nothing run", o.contentType, o.policy)
		}
	}
}
