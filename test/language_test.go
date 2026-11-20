package test

import (
	"bytes"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/mail/mailtest"
	"github.com/neonjelly-co/superit-drive/test/harness"
)

// English and Hungarian. The interface sends the language the person chose (or their browser's) as Accept-Language on
// every call; a browser sends its own on a page it opens. A visitor's page follows the browser, or the language the
// visitor chose with the page's switch, which the page's addresses then carry (?lang=); no cookie is set under /s/. A
// mail is in the language of the page whose act sends it: the share's, the invitation's, the code's.

// hungarian is a browser set to Hungarian, as a Hungarian's usually is.
const hungarian = "hu-HU,hu;q=0.9,en-US;q=0.8,en;q=0.7"

// A member's answers are in the language their page asks for: the same refusals, said in Hungarian when it asks for
// Hungarian, with the names in them as they are, and in English otherwise.
func TestAMembersAnswersAreInTheLanguageTheirPageAsksFor(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	en, hu := d.As(reka), d.As(reka).With("Accept-Language", hungarian)
	place, _ := myFiles(t, d, reka)
	madeFolder(t, en, place, 0, "Számlák")

	for _, c := range []struct {
		what       string
		ask        func(c *harness.Client) harness.Response
		status     int
		english    string
		hungarian  string
		hasNoValue bool
	}{
		{"an item that is not there", func(c *harness.Client) harness.Response {
			return c.Get(t, "/api/places/999999/items")
		}, http.StatusNotFound, "This is not in the Drive, or you have no access to it.",
			"Ez nincs a Drive-ban, vagy nincs hozzáférésed.", true},
		{"a name with a slash", func(c *harness.Client) harness.Response {
			return newFolder(t, c, place, 0, "a/b")
		}, http.StatusBadRequest, "A name cannot contain “/”. Choose another name.",
			"A név nem tartalmazhat „/” jelet. Válassz másik nevet.", true},
		{"a name that is taken", func(c *harness.Client) harness.Response {
			return newFolder(t, c, place, 0, "Számlák")
		}, http.StatusConflict, "There is already something called “Számlák” here. Choose another name.",
			"Itt már van valami ezzel a névvel: „Számlák”. Válassz másik nevet.", false},
		{"an empty search", func(c *harness.Client) harness.Response {
			return c.Get(t, "/api/search?q=%20")
		}, http.StatusBadRequest, "Type a part of a name to search for.", "Írd be egy név egy részét, és arra keresel.", true},
	} {
		got := c.ask(en)
		if got.Status != c.status || errorOf(t, got) != c.english {
			t.Errorf("%s, in English: got %d %s", c.what, got.Status, got.Body)
		}
		got = c.ask(hu)
		if got.Status != c.status || errorOf(t, got) != c.hungarian || got.Header.Get("Content-Language") != "hu" {
			t.Errorf("%s, in Hungarian: got %d %s (Content-Language %q)", c.what, got.Status, got.Body, got.Header.Get("Content-Language"))
		}
	}

	// Why guests cannot be invited, before anyone types: in the page's language.
	var me struct {
		Guests struct {
			Invite bool   `json:"invite"`
			Why    string `json:"why"`
		} `json:"guests"`
	}
	hu.Get(t, "/api/me").JSON(t, &me)
	if me.Guests.Invite || !strings.HasPrefix(me.Guests.Why, "Vendéget most nem lehet meghívni") ||
		!strings.Contains(me.Guests.Why, "Ennek a Drive-nak nincsenek levelezési beállításai") {
		t.Errorf("why guests cannot be invited, in Hungarian: %+v", me.Guests)
	}

	// Outside the API, a member's browser gets plain text in its own language.
	res := d.As(reka).With("Accept-Language", hungarian).Get(t, "/no/such/page")
	if res.Status != http.StatusNotFound || string(res.Body) != "Ezen a címen nincs semmi.\n" {
		t.Errorf("a page that is not there, in Hungarian: %d %q", res.Status, res.Body)
	}
	res = d.WithHeaders(http.Header{"Accept-Language": {hungarian}}).Get(t, "/api/places")
	if res.Status != http.StatusForbidden || errorOf(t, res) != "Ez a kérés nem a Drive bejelentkezésén keresztül érkezett." {
		t.Errorf("a request without the sign-in, in Hungarian: %d %s", res.Status, res.Body)
	}
}

// A visitor's pages are in their browser's language, or in the one they choose with the page's switch, which every
// address on the page then keeps, without a cookie. A link that does not work answers the same in each language.
func TestAVisitorsPagesAreInTheirLanguage(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	b := d.As(bence)
	x := bencesFiles(t, d)
	_, page := madeLink(t, d, b, x.place, x.clients.ID, false)
	hu := visitor(d).With("Accept-Language", hungarian)

	res := hu.Get(t, page)
	body := string(res.Body)
	for _, want := range []string{`<html lang="hu">`, "<p class=\"by\">Megosztotta: Bence Tóth</p>", ">Az összes letöltése ZIP-fájlként<",
		">Letöltés<", ">Megnyitás<", `<a href="?lang=en" hreflang="en" lang="en">In English</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the Hungarian page does not say %q:\n%s", want, body)
		}
	}
	if res.Header.Get("Content-Language") != "hu" || strings.Contains(body, "lang=hu") {
		t.Errorf("a page in the browser's language says so, and its addresses carry no choice: %q\n%s", res.Header.Get("Content-Language"), body)
	}
	visitorHeaders(t, "the Hungarian page", res)

	// Chosen with the switch: English, whatever the browser says, on this page and every page it leads to.
	res = hu.Get(t, page+"?lang=en")
	body = string(res.Body)
	if !strings.Contains(body, `<html lang="en">`) || !strings.Contains(body, "Shared by Bence Tóth") {
		t.Fatalf("the page chosen in English:\n%s", body)
	}
	hrefs := regexp.MustCompile(`href="(/s/[^"]*)"`).FindAllStringSubmatch(body, -1)
	if len(hrefs) == 0 {
		t.Fatalf("the page has no addresses:\n%s", body)
	}
	for _, h := range hrefs {
		href := html.UnescapeString(h[1])
		if !strings.HasSuffix(href, "lang=en") {
			t.Errorf("the address %s does not keep the language chosen", href)
		}
		if strings.Contains(href, "/download") || strings.Contains(href, "/zip") || strings.Contains(href, "/open") {
			continue
		}
		if next := hu.Get(t, href); !strings.Contains(string(next.Body), `<html lang="en">`) {
			t.Errorf("the page at %s is not in the language chosen:\n%s", href, next.Body)
		}
	}
	if res.Header.Get("Set-Cookie") != "" {
		t.Errorf("choosing a language set a cookie: %q", res.Header.Values("Set-Cookie"))
	}

	// A link that does not work: the one page, in Hungarian, the same bytes for a link never made and an item outside it.
	none := hu.Get(t, noSuchLink(t))
	outside := hu.Get(t, inLink(page, x.secret.ID, ""))
	if !strings.Contains(string(none.Body), "<h1>Ez a link nem működik</h1>") {
		t.Errorf("a link that does not work, in Hungarian:\n%s", none.Body)
	}
	sameAsNoLink(t, "an item outside the link, in Hungarian", outside, none)
	if english := visitor(d).Get(t, noSuchLink(t)); !strings.Contains(string(english.Body), "This link does not work") {
		t.Errorf("a link that does not work, in English:\n%s", english.Body)
	}
}

// An upload-only link's page is in the visitor's language, with one script whose hash is the same in every language:
// what it says is in the page. A file it sends carries the page's language, so the Drive's refusals are in it too.
func TestTheSendPageSpeaksTheVisitorsLanguageWithOneScript(t *testing.T) {
	t.Parallel()
	d := harness.Start(t, reka, bence)
	b := d.As(bence)
	x := bencesFiles(t, d)
	_, page := madeUploadOnly(t, d, b, x.place, x.clients.ID, false)

	en := visitor(d).Get(t, page)
	hu := visitor(d).With("Accept-Language", hungarian).Get(t, page)
	if en.Header.Get("Content-Security-Policy") != hu.Header.Get("Content-Security-Policy") ||
		!strings.Contains(hu.Header.Get("Content-Security-Policy"), "script-src 'sha256-") {
		t.Fatalf("the send page's policy differs between the languages:\n%s\n%s",
			en.Header.Get("Content-Security-Policy"), hu.Header.Get("Content-Security-Policy"))
	}
	scriptOf := regexp.MustCompile(`(?s)<script>(.*?)</script>`)
	if a, b := scriptOf.FindSubmatch(en.Body), scriptOf.FindSubmatch(hu.Body); a == nil || b == nil || !bytes.Equal(a[1], b[1]) {
		t.Fatalf("the send page's script differs between the languages")
	}
	body := html.UnescapeString(string(hu.Body))
	for _, want := range []string{`<html lang="hu">`, "Ebbe a mappába küldhetsz fájlokat.", "Húzd ide a fájlokat, vagy válaszd ki őket",
		"Fájlok kiválasztása", `"sendN":"{n} fájl küldése"`, `"waiting":"Sorára vár"`, `"decimal":","`, ">Küldés</button>"} {
		if !strings.Contains(body, want) {
			t.Errorf("the Hungarian send page does not say %q:\n%s", want, body)
		}
	}

	// A name a stranger may not send, refused in the language the page sent with it.
	turnedName := "Invoice\u202Efdp.exe"
	res := visitor(d).Do(t, http.MethodPost, sendPath(page, turnedName, newTurn())+"&lang=hu", strings.NewReader("x"))
	if res.Status != http.StatusBadRequest || !strings.HasPrefix(errorOf(t, res), "Ebben a névben olyan karakterek vannak") {
		t.Errorf("a name that turns the text, sent from a Hungarian page: %d %s", res.Status, res.Body)
	}
}

// A guest is spoken to in the language of the page they use, and their invitation and a member's "shared with you" in
// the language of the page the member shared from: the Drive knows no other language of someone it mails.
func TestMailIsInTheLanguageOfThePageWhoseActSendsIt(t *testing.T) {
	t.Parallel()
	smtp := mailtest.Start(t, mailtest.Options{})
	d := harness.StartWith(t, harness.Options{Members: []harness.Member{reka, bence, dora}, Env: mailSettings(smtp)})
	b := d.As(bence).With("Accept-Language", hungarian)
	x := bencesFiles(t, d)

	ok(t, "sharing Clients with Dóra from a Hungarian page", share(t, b, x.place, x.clients.ID, dora, "editor"))
	got := readMail(t, smtp.WaitFor(t, 1)[0])
	if got.subject != "Bence Tóth megosztott veled valamit: „Clients”" ||
		!strings.HasPrefix(got.body, "Bence Tóth megosztott veled egy mappát: „Clients”. Projekt: ") ||
		!strings.Contains(got.body, "Szerkesztőként fel is tölthetsz") || !strings.Contains(got.body, "\nMegnyitás: http") {
		t.Errorf("the share mail from a Hungarian page:\nSubject: %s\n\n%s", got.subject, got.body)
	}

	ends := time.Now().Add(72 * time.Hour)
	invited(t, b, x.place, x.offer.ID, anna, "viewer", &ends)
	var invitation mailed
	harness.Eventually(t, "the invitation arrives", func() bool {
		m := mailsTo(t, smtp, anna)
		if len(m) > 0 {
			invitation = m[0]
		}
		return len(m) > 0
	})
	if invitation.subject != "Bence Tóth megosztott veled valamit: „Offer.pdf”" ||
		!strings.Contains(invitation.body, "megosztott veled egy fájlt: „Offer.pdf”") ||
		!strings.Contains(invitation.body, "A hozzáférésed eddig tart: "+ends.UTC().Format("2006")+". ") ||
		!strings.Contains(invitation.body, "Fiók nem kell hozzá.") {
		t.Errorf("the invitation from a Hungarian page:\nSubject: %s\n\n%s", invitation.subject, invitation.body)
	}

	// The guest asks for a code from a Hungarian page: told in Hungarian, and mailed in Hungarian.
	guest := browser(d).With("Accept-Language", hungarian)
	res := askForCode(t, guest, anna)
	var asked struct {
		Message string `json:"message"`
	}
	res.JSON(t, &asked)
	if res.Status != http.StatusAccepted || !strings.HasPrefix(asked.Message, "Ha ezen a Drive-on bármi meg van osztva") {
		t.Errorf("asking for a code from a Hungarian page: %d %s", res.Status, res.Body)
	}
	var code mailed
	harness.Eventually(t, "the code arrives", func() bool {
		m := mailsTo(t, smtp, anna)
		if len(m) > 1 {
			code = m[1]
		}
		return len(m) > 1
	})
	if !strings.HasPrefix(code.subject, "Belépési kód a Drive-hoz (") || !regexp.MustCompile(`^A kódod: \d{6}\n`).MatchString(code.body) {
		t.Errorf("the code from a Hungarian page:\nSubject: %s\n\n%s", code.subject, code.body)
	}
	if res := signIn(t, guest, anna, "000000"); res.Status != http.StatusBadRequest ||
		errorOf(t, res) != "Ez a kód nem jó, vagy már nem érvényes. Ellenőrizd, vagy kérj újat." {
		t.Errorf("a wrong code from a Hungarian page: %d %s", res.Status, res.Body)
	}
	if res := guest.Get(t, guestAPI+"/shared-with-me"); res.Status != http.StatusUnauthorized || errorOf(t, res) != "Jelentkezz be, hogy ezt lásd." {
		t.Errorf("a guest not signed in, on a Hungarian page: %d %s", res.Status, res.Body)
	}

	// The same from English pages: English.
	ok(t, "sharing Notes with Réka from an English page", share(t, d.As(bence), x.place, x.notes.ID, reka, "viewer"))
	harness.Eventually(t, "the English share mail arrives", func() bool { return len(mailsTo(t, smtp, reka.Email)) > 0 })
	if m := mailsTo(t, smtp, reka.Email)[0]; m.subject != "Bence Tóth shared “Notes.txt” with you" {
		t.Errorf("the share mail from an English page: %s", m.subject)
	}
}
