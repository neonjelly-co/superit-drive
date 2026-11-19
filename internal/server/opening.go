package server

import "strings"

// Opening a file in the browser, and the headers on every answer that carries a file's bytes.
//
// A file a member uploaded is served from the Drive's own address, where the sign-in cookie applies: an HTML, SVG, XML
// or script file shown as a page there would run as the Drive, with the person's access. So only a fixed list of kinds
// opens in the browser (an ordinary image, a PDF, plain text, a video, a sound), decided by the extension of the file's
// name and nothing else, and each is sent with the one content type the list gives it: never a type taken from the
// upload, the storage or the bytes. Everything else is sent as application/octet-stream, to be saved. Opening and
// downloading are separate addresses (…/open and …/download): a download never opens, whatever is asked of it.
//
// Every such answer also carries X-Content-Type-Options: nosniff (securityHeaders), so the browser never guesses
// another type from the bytes, and a Content-Security-Policy under which nothing runs (sandboxed, nothing allowed to
// load). Measured in Chrome 157 and Firefox: images, PDFs (the browser's own viewer) and text show in a sandboxed page
// with an opaque origin. A video or a sound plays in a page the browser makes around it, which fetches the bytes again
// (with Range, to seek) as media from the Drive's address: from an opaque origin Chrome does not play it (and the
// Drive's Cross-Origin-Resource-Policy would refuse it), so those keep their origin (allow-same-origin, still with no
// scripts, forms or anything else) and may load media from it, and nothing else.

// An opening is how a kind of file opens in the browser: the content type sent with it, and the policy it is shown
// under.
type opening struct {
	contentType string
	policy      string
}

const (
	// sandboxed is the policy of everything that carries a file's bytes, but a video or a sound: nothing may load or
	// run, and the page has an opaque origin, so not even a browser's mistake could act as the Drive.
	sandboxed = "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox"
	// playable is the policy of a video or a sound opened in the browser: nothing may run, and the only thing that may
	// load is media from the Drive's own address (the file itself, again, as the player seeks).
	playable = "default-src 'none'; media-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox allow-same-origin"
)

// opensInBrowser is every kind of file that opens in the browser, by the extension of its name in lower case. Nothing
// else does: not a web page (.html, .xhtml, .htm), not an SVG image, not XML, not a script, nor anything this list does
// not name. Formats were kept only where Chrome showed or played them (TIFF, QuickTime .mov and Ogg Theora video do
// not, and download instead).
var opensInBrowser = map[string]opening{
	// Ordinary images.
	".jpg":  {"image/jpeg", sandboxed},
	".jpeg": {"image/jpeg", sandboxed},
	".png":  {"image/png", sandboxed},
	".gif":  {"image/gif", sandboxed},
	".webp": {"image/webp", sandboxed},
	".avif": {"image/avif", sandboxed},
	".bmp":  {"image/bmp", sandboxed},
	// PDF, in the browser's own viewer.
	".pdf": {"application/pdf", sandboxed},
	// Plain text. Its encoding is not known; UTF-8 is the one most text is in.
	".txt": {"text/plain; charset=utf-8", sandboxed},
	".log": {"text/plain; charset=utf-8", sandboxed},
	".md":  {"text/plain; charset=utf-8", sandboxed},
	// Video.
	".mp4":  {"video/mp4", playable},
	".m4v":  {"video/mp4", playable},
	".webm": {"video/webm", playable},
	// Sound.
	".mp3":  {"audio/mpeg", playable},
	".m4a":  {"audio/mp4", playable},
	".aac":  {"audio/aac", playable},
	".wav":  {"audio/wav", playable},
	".flac": {"audio/flac", playable},
	".ogg":  {"audio/ogg", playable},
	".oga":  {"audio/ogg", playable},
	".opus": {"audio/ogg", playable},
}

// openingOf is how a file called name opens in the browser; ok is false when it does not, and downloads instead. Only
// the extension counts: the part of the name after its last dot, which must not be its first character (".png" alone
// is a name, not an extension), in plain ASCII letters and digits of any case.
func openingOf(name string) (o opening, ok bool) {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 {
		return opening{}, false
	}
	ext := []byte(name[i:])
	for j := 1; j < len(ext); j++ {
		switch c := ext[j]; {
		case 'A' <= c && c <= 'Z':
			ext[j] = c + ('a' - 'A')
		case 'a' <= c && c <= 'z', '0' <= c && c <= '9':
		default:
			return opening{}, false // anything else, a space or a letter outside ASCII included, is no extension of the list
		}
	}
	o, ok = opensInBrowser[string(ext)]
	return o, ok
}

// opensInTheBrowser says whether a file called name opens in the browser.
func opensInTheBrowser(name string) bool {
	_, ok := openingOf(name)
	return ok
}
