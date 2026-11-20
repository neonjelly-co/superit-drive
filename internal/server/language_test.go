package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/internal/text"
)

// Every text the server, the Drive's operations and its mail write for a person is in English and in Hungarian, the
// Hungarian translated (not the English again) and saying the same values. This binary holds the messages of every
// package the server uses.
func TestEveryTextAPersonReadsIsInEnglishAndHungarian(t *testing.T) {
	all := text.All()
	if len(all) < 150 {
		t.Fatalf("only %d messages are known: the server's, the Drive's and the mail's should all be here", len(all))
	}
	for _, m := range all {
		if p := m.Problem(); p != "" {
			t.Error(p)
		}
	}
}

// sentence is a string that reads as words for a person: a capital, then at least two more words.
var sentence = regexp.MustCompile(`^[“"„]?[A-Z][a-z']*,?( [a-z“„'’][^ ]*){2,}`)

// No sentence for a person is written in the server, the Drive's operations or its mail outside a text.M: each is said
// in the request's language, and none can slip past in English. Log lines, errors (which only the log reads) and panics
// are for the people running the Drive, and stay in English.
func TestNoTextForAPersonIsWrittenAroundTheLanguages(t *testing.T) {
	for _, dir := range []string{".", "../drive", "../mail"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			skip := map[ast.Node]bool{}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if forTheLog(call.Fun) {
					for _, a := range call.Args {
						ast.Inspect(a, func(m ast.Node) bool { skip[m] = true; return true })
					}
				}
				return true
			})
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING || skip[n] {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				if sentence.MatchString(s) {
					t.Errorf("%s: %q is written for a person outside text.M, so it is only in English", fset.Position(lit.Pos()), s)
				}
				return true
			})
		}
	}
}

// forTheLog says whether a call's words are for the log, an error or a panic, or are a message's two languages.
func forTheLog(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name == "panic"
	case *ast.SelectorExpr:
		if x, ok := f.X.(*ast.Ident); ok {
			switch x.Name + "." + f.Sel.Name {
			case "text.M", "errors.New", "fmt.Errorf", "slog.Info", "slog.Warn", "slog.Error":
				return true
			}
		}
		switch f.Sel.Name {
		case "Info", "Warn", "Error", "Debug":
			return true
		}
	}
	return false
}

// The visitors' pages say nothing of their own: every word on them is the language's, so a page is never partly in
// another. What the template holds outside its actions and tags is the brand's name and punctuation.
func TestTheVisitorsTemplateHoldsNoWordsOfItsOwn(t *testing.T) {
	bare := regexp.MustCompile(`(?s)\{\{.*?\}\}`).ReplaceAllString(visitorHTML, " ")
	bare = regexp.MustCompile(`(?s)<style>.*?</style>|<script>.*?</script>|<[^>]*>`).ReplaceAllString(bare, " ")
	bare = strings.ReplaceAll(bare, "Superit Drive", " ")
	if words := regexp.MustCompile(`\pL+`).FindAllString(bare, -1); len(words) > 0 {
		t.Fatalf("the visitors' template says %q itself, in no language", words)
	}
}

// A visitor's pages are in the language their browser prefers, or the one they chose with the page's switch, which
// the addresses on the page then keep; the "not here" page is the same bytes for every link that does not work, in
// each language.
func TestAVisitorsPageIsInTheirLanguage(t *testing.T) {
	s := bareServer(t)
	page := func(target, accept string, v visitorView) string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Accept-Language", accept)
		s.renderVisitor(rec, req, http.StatusOK, v)
		return rec.Body.String()
	}
	v := visitorView{Name: "Invoices", By: "Bence", Folder: true, Items: []visitorRow{{Name: "a.pdf", Href: "/s/t/items/2/download"}}}
	hu := page("/s/t", "hu-HU,hu;q=0.9,en;q=0.5", v)
	for _, want := range []string{`<html lang="hu">`, "Az összes letöltése ZIP-fájlként", ">Letöltés<", `href="?lang=en"`, ">In English<"} {
		if !strings.Contains(hu, want) {
			t.Errorf("the Hungarian page does not say %q:\n%s", want, hu)
		}
	}
	en := page("/s/t", "en-GB", v)
	for _, want := range []string{`<html lang="en">`, "Download all as zip", ">Download<", `href="?lang=hu"`, ">Magyarul<"} {
		if !strings.Contains(en, want) {
			t.Errorf("the English page does not say %q:\n%s", want, en)
		}
	}
	if chosen := page("/s/t?lang=en", "hu", v); !strings.Contains(chosen, `<html lang="en">`) {
		t.Errorf("a visitor who chose English gets:\n%s", chosen)
	}
	notHere := func(target, accept string) string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Accept-Language", accept)
		s.visitorRefusal(rec, req, http.StatusNotFound, notHere)
		return rec.Body.String()
	}
	if a, b := notHere("/s/one", "hu"), notHere("/s/other/items/3", "hu"); a != b || !strings.Contains(a, "Ez a link nem működik") {
		t.Errorf("two links that do not work answer differently, or not in Hungarian:\n%s\n%s", a, b)
	}
}
