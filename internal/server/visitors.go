package server

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/members"
	"github.com/neonjelly-co/superit-drive/internal/text"
)

// The public prefix: what visitors reach with a public link, without signing in. Its routes are in routes.go with all
// the others, and pass guard like them; guardVisitor is guard's part for them.
//
// A request under the public prefix is a visitor's, always: nothing it carries about who sent it (Drive-Secret,
// Remote-User-Id, cookies) is read, so a signed-in member who opens a link is a visitor like anyone else, and a forged
// identity does nothing. The reverse proxy opens the prefix without sign-in and removes those headers anyway. A visitor
// is decided by the access package as a person of their own kind, through the link their address names, with its item
// as the top of what they may see; no route outside the prefix ever decides for a visitor (a test checks both).
//
// Visitors only read, but for one thing: every route here is GET (and HEAD), and any other method is refused with 405
// before anything else is looked at; the one exception is sending a file in through an upload-only link (POST
// /s/{token}/files, into the link's own folder, never replacing anything), and only that route takes a POST. Every
// answer here is marked so that the address, which is the secret, does not travel and is not kept (Referrer-Policy:
// no-referrer, Cache-Control: no-store), search engines do not list it (X-Robots-Tag), and nothing runs or loads but the
// page's own style (visitorPolicy); no cookie is ever set. A link that does not work (removed, never made, malformed, or
// one that does not count now), and anything outside what it covers, gets the one answer visitorNotHere, the same bytes
// for every one of them.
//
// The pages are plain HTML made here, one column, with nothing of the members' interface. A view link's pages have no
// script: they work without JavaScript. An upload-only link's page has one small script of its own, in the page, which
// sends each file chosen or dropped as one request (sendPolicy lets it run, by its hash, and talk to the public prefix
// of the Drive's own address, and nothing else). A file's bytes are sent by the members' own handlers (download, open,
// zip), with the same safe list and headers (opening.go); they, and files sent in, take a transfer slot in the
// outsiders' own line (at most transfers.OutsiderSlots of them at once, with guests).

// How many visitors' requests are worked on at once, how many more may wait for their turn, and for how long; any more
// are told at once that the Drive is busy. A transfer leaves this count once it is decided, to wait for its slot in
// the visitors' own line (package transfers), which has its own bound. So a crowd from outside costs a bounded number
// of goroutines, index connections and buffers, however many links it tries.
const (
	visitorsAtOnce  = 16
	visitorsWaiting = 64
	visitorWait     = 5 * time.Second
)

// visitorListLimit is the most items a visitor's page lists of one folder; the zip holds them all.
const visitorListLimit = 1000

// guardVisitor is guard for a route under the public prefix: only reading (or, on the one route for it, sending a file
// in), then the visitor's link, the place its item is in, the item the request is about and the access given on the way
// down to it, then access.Decide for the visitor.
func (s *Server) guardVisitor(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.action == access.UploadThroughLink {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			s.refuse(w, r, http.StatusMethodNotAllowed, onlyReading)
			return
		}
	} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		s.refuse(w, r, http.StatusMethodNotAllowed, onlyReading)
		return
	}
	leave, ok := s.visitors.enter(r.Context())
	if !ok {
		if r.Context().Err() == nil {
			w.Header().Set("Retry-After", "5")
			s.refuse(w, r, http.StatusServiceUnavailable, visitorsBusy)
		}
		return
	}
	// The answer is kept while the turn is held, and sent once it is given back (answers.go): a visitor who does not
	// take their page keeps nobody else waiting.
	held := &heldAnswer{ResponseWriter: w, most: visitorAnswerMost}
	defer func() { leave(); s.sendHeld(r, held) }()
	c, ok := s.throughLink(held, r, rt)
	if !ok {
		return
	}
	if !access.Decide(c.person, rt.action, c.access) || rt.serve == nil {
		if rt.serve != nil && access.SwitchedOff(c.person, rt.action, c.access) {
			// The token matched, and the link would open this: it is not "not here", only not now.
			s.switchedOff(held, r, c)
			return
		}
		s.refuse(held, r, http.StatusNotFound, notHere)
		return
	}
	if rt.action == access.DownloadThroughLink || rt.action == access.UploadThroughLink {
		leave() // it waits for its transfer slot in the outsiders' own line, which bounds them by itself
		rt.serve(w, r, c)
		return
	}
	rt.serve(held, r, c)
}

// throughLink finds, for a visitor, the link their address names (whatever the token is: see drive.LinkByToken), the
// place its item is in, the item the request is about (the route's, or the link's own), and the access given on the
// place and on the way down to it. ok is false when they were already answered: anything that is not there gets the
// same answer as a link that does not exist.
func (s *Server) throughLink(w http.ResponseWriter, r *http.Request, rt route) (c call, ok bool) {
	ctx := r.Context()
	link, err := s.drive.LinkByToken(ctx, r.PathValue(rt.link))
	if errors.Is(err, index.ErrNoLink) {
		s.refuse(w, r, http.StatusNotFound, notHere)
		return call{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return call{}, false
	}
	p, err := s.drive.Place(ctx, link.Place)
	if errors.Is(err, index.ErrNoPlace) {
		s.refuse(w, r, http.StatusNotFound, notHere) // a shared folder in the trash
		return call{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return call{}, false
	}
	var it index.Item
	if rt.item != "" {
		it, err = s.findItem(r, p, rt.item)
	} else {
		it, err = s.drive.Item(ctx, p, link.Item)
	}
	if errors.Is(err, index.ErrNoItem) {
		s.refuse(w, r, http.StatusNotFound, notHere) // in the trash, gone, or in another place
		return call{}, false
	}
	if err != nil {
		s.fail(w, r, err)
		return call{}, false
	}
	where, err := s.accessAt(ctx, p, idsOf(pathTo(it)))
	if err != nil {
		s.fail(w, r, err)
		return call{}, false
	}
	return call{link: &link, person: visitorOf(link, s.members.Current()), place: &p, item: &it, access: where}, true
}

// visitorOf is the visitor who came with link, as the access package decides on them.
func visitorOf(l index.Link, list *members.List) access.Person {
	a := accessLink(l, list)
	return access.Person{Link: &a}
}

// accessLink is a link as the access package needs it, with its maker as the member list has them now.
func accessLink(l index.Link, list *members.List) access.Link {
	by := access.Person{UserID: l.MadeBy}
	if m, ok := list.Lookup(l.MadeBy); ok {
		by = personOf(m)
	}
	return access.Link{Item: l.Item, Kind: access.LinkKind(l.Kind), Folder: l.Folder, By: by}
}

// used counts a use of the link a visitor came with, or of the invitations a guest came in through: in memory, so it
// never waits, takes no lock and never fails (drive.UsedLink, drive.UsedInvitation). A use is a page through the
// link, a folder or file a guest opened, or a file's bytes (a download, a file opened, a zip) from their start: not a
// HEAD, nor a part of a file after its start (a video seeking, a download resuming); and a guest's change (an upload, a
// new folder, a rename, a move, a delete). Each of the guest's invitations on the way down to what they used counts,
// while it lets them in.
func (s *Server) used(r *http.Request, c call) {
	if r.Method == http.MethodHead {
		return
	}
	if c.link != nil {
		s.drive.UsedLink(*c.link)
	}
	if c.guest != nil && c.access != nil {
		list := s.members.Current()
		for _, g := range c.grants {
			if g.Guest == c.guest.ID && invitationWorks(c.access, accessGrant(g, list)) {
				s.drive.UsedInvitation(g)
			}
		}
	}
}

// visitorPage is the page of what a link is on, or of a folder inside it: who shared it, its name, and, for a folder,
// "Download all as zip" and what is in it (folders open their own page; files open, when their kind does, and
// download); for a file, Open and Download. Above a folder inside it are the folders from the link's item down: never
// anything above the link's item.
//
// Through an upload-only link, the link's own page is the place to send files in (sendInPage): nothing of what is in the
// folder.
//
//	GET /s/{token}
//	GET /s/{token}/items/{item}
func (s *Server) visitorPage(w http.ResponseWriter, r *http.Request, c call) {
	if !access.Decide(c.person, access.SeeThroughLink, c.access) {
		if access.Decide(c.person, access.UploadThroughLink, c.access) {
			s.sendInPage(w, r, c)
			return
		}
		s.refuse(w, r, http.StatusNotFound, notHere) // guard decided only on opening the link's page; never
		return
	}
	token := r.PathValue("token")
	l, keep := text.Of(r), keepLang(r)
	path := pathTo(*c.item)
	at := slices.IndexFunc(path, func(it index.Item) bool { return it.ID == c.link.Item })
	if at < 0 {
		s.refuse(w, r, http.StatusNotFound, notHere) // access decided it is inside; never
		return
	}
	here := func(id int64) string {
		if id == c.link.Item {
			return keep(PublicPrefix + token)
		}
		return keep(PublicPrefix + token + "/items/" + strconv.FormatInt(id, 10))
	}
	v := visitorView{Name: c.item.Name, By: s.sharer(*c.link, l), Folder: c.item.Folder}
	for _, f := range path[at : len(path)-1] {
		v.Crumbs = append(v.Crumbs, visitorLink{Name: f.Name, Href: here(f.ID)})
	}
	if c.item.Folder {
		items, more, err := s.drive.FirstChildren(r.Context(), *c.place, c.item, visitorListLimit)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v.Zip = keep(PublicPrefix + token + "/items/" + strconv.FormatInt(c.item.ID, 10) + "/zip")
		// Folders first, then by name as people read it: without regard to case or accents.
		slices.SortStableFunc(items, func(a, b index.Item) int {
			if a.Folder != b.Folder {
				if a.Folder {
					return -1
				}
				return 1
			}
			return cmp.Or(strings.Compare(index.SearchKey(a.Name), index.SearchKey(b.Name)), strings.Compare(a.Name, b.Name))
		})
		for _, it := range items {
			row := visitorRow{Name: it.Name, Folder: it.Folder, Href: here(it.ID)}
			if !it.Folder {
				row.Size = text.Size(l, it.Size)
				row.Href = keep(PublicPrefix + token + "/items/" + strconv.FormatInt(it.ID, 10) + "/download")
				if opensInTheBrowser(it.Name) {
					row.Open = keep(PublicPrefix + token + "/items/" + strconv.FormatInt(it.ID, 10) + "/open")
				}
			}
			v.Items = append(v.Items, row)
		}
		if more {
			v.More = onlyTheFirst.In(l, len(items))
		}
	} else {
		v.Size = text.Size(l, c.item.Size)
		v.Download = keep(PublicPrefix + token + "/items/" + strconv.FormatInt(c.item.ID, 10) + "/download")
		if opensInTheBrowser(c.item.Name) {
			v.Open = keep(PublicPrefix + token + "/items/" + strconv.FormatInt(c.item.ID, 10) + "/open")
		}
	}
	s.renderVisitor(w, r, http.StatusOK, v)
	s.used(r, c)
}

// sharer says who shared what a link is on, as a visitor reads it in the language l: the display name of the member who
// made it, and never their email address or user id. Without a name (they have none, or have left the project), the
// project's.
func (s *Server) sharer(l index.Link, in text.Lang) string {
	if m, ok := s.members.Current().Lookup(l.MadeBy); ok && strings.TrimSpace(m.DisplayName) != "" {
		return sharedBy.In(in, strings.TrimSpace(m.DisplayName))
	}
	return sharedFrom.In(in, s.project)
}

// keepLang is what keeps a visitor's choice of language from page to page: once they chose one (the page's switch adds
// "lang" to its address), every address on the page carries it too. Without a choice the addresses are as they are,
// and each page follows the browser's language. No cookie is set or read under the public prefix.
func keepLang(r *http.Request) func(href string) string {
	l, ok := text.Chosen(r)
	if !ok {
		return func(href string) string { return href }
	}
	return func(href string) string {
		if strings.Contains(href, "?") {
			return href + "&lang=" + l.Code()
		}
		return href + "?lang=" + l.Code()
	}
}

// visitorRefusal answers a visitor with a page saying why, in their language. Every "not here" is the one page for a
// link that does not work, whatever it was about: the same bytes in each language.
func (s *Server) visitorRefusal(w http.ResponseWriter, r *http.Request, status int, message text.Message, values ...any) {
	l := text.Of(r)
	v := visitorView{Heading: didNotWork.In(l), Message: message.In(l, values...)}
	if message == notHere {
		v = visitorView{Heading: linkDoesNotWork.In(l), Message: visitorNotHere.In(l)}
	}
	s.renderVisitor(w, r, status, v)
}

// visitorView is what a visitor's page shows.
type visitorView struct {
	// Heading and Message are for a page that says why something did not work, instead of the rest.
	Heading, Message string

	// By says who shared it.
	By string
	// Crumbs are the folders from the link's item down to the folder shown, without it; none at the link's item.
	Crumbs []visitorLink
	// Name is the name of the file or folder shown.
	Name   string
	Folder bool
	// For a folder: the zip of it, what is in it, and, when there is more than is listed, a line that says so.
	Zip   string
	Items []visitorRow
	More  string
	// For a file: its size, and where it opens (when its kind does) and downloads.
	Size, Open, Download string
	// Send is true for an upload-only link's page: a place to choose or drop files and send them into the folder, and
	// nothing of what is in it; Full when the link has taken in all it may, and the page says so instead.
	Send, Full bool
}

type visitorLink struct{ Name, Href string }

// visitorRow is one item of a folder on a visitor's page: a folder's Href is its page, a file's its download.
type visitorRow struct {
	Name       string
	Folder     bool
	Href       string
	Size, Open string
}

// renderVisitor sends a visitor's page in the request's language (text.Of): its words, its lang, and a switch to the
// other language that keeps to the same page.
func (s *Server) renderVisitor(w http.ResponseWriter, r *http.Request, status int, v visitorView) {
	l := text.Of(r)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Language", l.Code())
	if v.Send && !v.Full {
		h.Set("Content-Security-Policy", s.sendPolicy)
	}
	w.WriteHeader(status)
	if err := visitorTemplate.Execute(w, struct {
		visitorView
		Lang      string
		W         visitorWords
		SendWords string
		Style     template.CSS
		Script    template.JS
		Title     string
		FullNote  string
	}{v, l.Code(), visitorWordsOf[l], sendWordsOf[l], template.CSS(visitorStyle), template.JS(sendScript), titleOf(v),
		linkFull.In(l)}); err != nil {
		s.log.Info("a visitor's page was not sent whole", "error", err)
	}
}

func titleOf(v visitorView) string {
	if v.Heading != "" {
		return v.Heading
	}
	return v.Name
}

// visitorStyle is the visitors' pages' only style, in the page itself: Pilot light's mist ground, white panel and
// graphite ink, in one column on every screen. Everything a finger taps is at least 44 px tall. On a screen without a
// mouse (a phone), the place to send files says "Choose files", since nothing is dragged there.
const visitorStyle = `:root{color-scheme:light;--background:oklch(0.956 0.006 239.8);--foreground:oklch(0.283 0.026 246.5);` +
	`--card:oklch(1 0 0);--muted:oklch(0.937 0.008 241.7);--muted-foreground:oklch(0.511 0.028 248.4);` +
	`--border:oklch(0.893 0.013 244.3);--ring:oklch(0.511 0.028 248.4);--destructive:oklch(0.544 0.186 26)}` +
	`*{box-sizing:border-box}` +
	`html,body{margin:0;background:var(--background);color:var(--foreground);font-family:"Atkinson Hyperlegible Next",` +
	`"Atkinson Hyperlegible",ui-sans-serif,system-ui,sans-serif;line-height:1.5;-webkit-text-size-adjust:100%}` +
	`body{min-height:100vh;display:flex;flex-direction:column}` +
	`main{width:100%;max-width:44rem;margin:0 auto;padding:2.5rem 1rem 2rem;flex:1}` +
	`.by{margin:0;color:var(--muted-foreground);overflow-wrap:anywhere}` +
	`.crumbs{margin:.5rem 0 0;color:var(--muted-foreground);overflow-wrap:anywhere}` +
	`.crumbs a{display:inline-flex;align-items:center;min-height:2.75rem;color:inherit}` +
	`h1{margin:.25rem 0 0;font-size:1.625rem;line-height:1.25;font-weight:700;overflow-wrap:anywhere}` +
	`.actions{display:flex;flex-wrap:wrap;gap:.5rem;margin:1.25rem 0 0}` +
	`.button{display:inline-flex;align-items:center;min-height:2.75rem;padding:.4rem 1rem;border-radius:.625rem;` +
	`background:var(--foreground);color:#fff;text-decoration:none;font-weight:600}` +
	`.button.quiet{background:var(--card);color:var(--foreground);border:1px solid var(--border)}` +
	`a:focus-visible{outline:3px solid var(--ring);outline-offset:2px}` +
	`.list{list-style:none;margin:1.5rem 0 0;padding:0;background:var(--card);border:1px solid var(--border);` +
	`border-radius:.75rem;overflow:hidden}` +
	`.list li{display:flex;flex-wrap:wrap;align-items:center;gap:0 .75rem;padding:.25rem 1rem;` +
	`border-top:1px solid var(--border)}` +
	`.list li:first-child{border-top:0}` +
	`.name{flex:1 1 12rem;min-width:0;min-height:2.75rem;display:flex;gap:.625rem;align-items:center;overflow-wrap:anywhere}` +
	`a.name{color:inherit;text-decoration:none;font-weight:600}a.name:hover{text-decoration:underline}` +
	`.icon{width:1.25rem;height:1.25rem;flex:none;color:var(--muted-foreground)}` +
	`.size{color:var(--muted-foreground);font-variant-numeric:tabular-nums;white-space:nowrap}` +
	`.do{display:flex;gap:.5rem;white-space:nowrap}` +
	`.do a{display:inline-flex;align-items:center;min-height:2.75rem;padding:0 .25rem;color:var(--foreground);font-weight:600}` +
	`.note{margin:1rem 0 0;color:var(--muted-foreground)}` +
	`.send{margin:1.5rem 0 0}` +
	`.drop{position:relative;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:.375rem;` +
	`min-height:10rem;padding:1.5rem 1rem;border:2px dashed var(--border);border-radius:.75rem;background:var(--card);` +
	`text-align:center}` +
	`.drop:hover,.drop.over{border-color:var(--ring)}` +
	`.drop:focus-within{outline:3px solid var(--ring);outline-offset:2px}` +
	`.drop input{position:absolute;inset:0;width:100%;height:100%;opacity:0;cursor:pointer}` +
	`.drop strong{font-weight:600}.drop span{color:var(--muted-foreground)}` +
	`.drop .touch{display:none}` +
	`@media (hover:none) and (pointer:coarse){.drop .mouse{display:none}.drop .touch{display:inline}}` +
	`.send .list .state{color:var(--muted-foreground);font-variant-numeric:tabular-nums;overflow-wrap:anywhere}` +
	`.send .list .state.bad{color:var(--destructive)}` +
	`button.button{border:0;font:inherit;cursor:pointer}button.button:disabled{opacity:.5;cursor:default}` +
	`button:focus-visible{outline:3px solid var(--ring);outline-offset:2px}` +
	`footer{padding:1rem;text-align:center;font-size:.875rem;color:var(--muted-foreground)}` +
	`footer a{display:inline-flex;align-items:center;min-height:2.75rem;padding:0 .5rem;color:inherit}`

// visitorPolicy is the Content-Security-Policy of every answer under the public prefix that is not a file's bytes
// (those carry opening.go's): nothing may load or run but the page's own style, no form goes anywhere, nothing may frame
// it.
var visitorPolicy = "default-src 'none'; style-src '" + styleHash(visitorStyle) + "'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func styleHash(css string) string {
	sum := sha256.Sum256([]byte(css))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

const folderIcon = `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" ` +
	`stroke-linejoin="round" aria-hidden="true"><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 ` +
	`7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>`

const fileIcon = `<svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" ` +
	`stroke-linejoin="round" aria-hidden="true"><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/>` +
	`<path d="M14 2v4a2 2 0 0 0 2 2h4"/></svg>`

// visitorTemplate is every visitor's page (visitorHTML).
var visitorTemplate = template.Must(template.New("visitor").Funcs(template.FuncMap{
	"folderIcon": func() template.HTML { return folderIcon },
	"fileIcon":   func() template.HTML { return fileIcon },
}).Parse(visitorHTML))

// visitorHTML is the template of every visitor's page. It holds no words of its own: they are the language's (W, and
// the view's own), so that a page is all in one language (a test checks that it says nothing outside them).
const visitorHTML = `<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<meta name="referrer" content="no-referrer">
<title>{{.Title}}</title>
<style>{{.Style}}</style>
</head>
<body>
<main>
{{- if .Message}}
<h1>{{.Heading}}</h1>
<p class="note">{{.Message}}</p>
{{- else}}
<p class="by">{{.By}}</p>
{{- if .Crumbs}}
<nav class="crumbs" aria-label="{{.W.Folders}}">{{range .Crumbs}}<a href="{{.Href}}">{{.Name}}</a> › {{end}}</nav>
{{- end}}
<h1>{{.Name}}</h1>
{{- if .Send}}
<p class="note">{{.W.SendIntro}}</p>
{{- if .Full}}
<p class="note" id="full">{{.FullNote}}</p>
{{- else}}
<div class="send" id="sending" data-words="{{.SendWords}}">
<label class="drop" id="drop"><input type="file" id="files" multiple><strong><span class="mouse">{{.W.DropOrChoose}}</span><span class="touch">{{.W.ChooseFiles}}</span></strong><span>{{.W.FilesOnly}}</span></label>
<ul class="list" id="chosen" hidden></ul>
<p class="actions"><button class="button" id="send" type="button" disabled>{{.W.Send}}</button></p>
<p class="note" id="said" role="status" aria-live="polite"></p>
</div>
<noscript><p class="note">{{.W.NeedsScript}}</p></noscript>
<script>{{.Script}}</script>
{{- end}}
{{- else if .Folder}}
<p class="actions"><a class="button" href="{{.Zip}}" download>{{.W.DownloadAll}}</a></p>
{{- if .Items}}
<ul class="list">
{{- range .Items}}
{{- if .Folder}}
<li><a class="name" href="{{.Href}}">{{folderIcon}}<span>{{.Name}}</span></a></li>
{{- else}}
<li><span class="name">{{fileIcon}}<span>{{.Name}}</span></span><span class="size">{{.Size}}</span><span class="do">{{if .Open}}<a href="{{.Open}}" target="_blank" rel="noopener">{{$.W.Open}}</a>{{end}}<a href="{{.Href}}" download>{{$.W.Download}}</a></span></li>
{{- end}}
{{- end}}
</ul>
{{- if .More}}
<p class="note">{{.More}}</p>
{{- end}}
{{- else}}
<p class="note">{{.W.Empty}}</p>
{{- end}}
{{- else}}
<p class="note">{{.Size}}</p>
<p class="actions">{{if .Open}}<a class="button quiet" href="{{.Open}}" target="_blank" rel="noopener">{{.W.Open}}</a>{{end}}<a class="button" href="{{.Download}}" download>{{.W.Download}}</a></p>
{{- end}}
{{- end}}
</main>
<footer>Superit Drive · <a href="?lang={{.W.OtherCode}}" hreflang="{{.W.OtherCode}}" lang="{{.W.OtherCode}}">{{.W.Other}}</a></footer>
</body>
</html>
`

// admission lets at most a fixed number of requests be worked on at once, and a fixed number more wait for their turn,
// each for at most a while. Any more are refused at once.
type admission struct {
	slots      chan struct{}
	waiting    atomic.Int64
	maxWaiting int64
	wait       time.Duration
}

func newAdmission(atOnce, waiting int, wait time.Duration) *admission {
	return &admission{slots: make(chan struct{}, atOnce), maxWaiting: int64(waiting), wait: wait}
}

// enter takes a turn, waiting for one if need be. ok is false when there was none in time, too many wait already, or
// ctx ended. Call leave when done; once is enough, and more do nothing.
func (a *admission) enter(ctx context.Context) (leave func(), ok bool) {
	select {
	case a.slots <- struct{}{}:
		return a.leaver(), true
	default:
	}
	if a.waiting.Add(1) > a.maxWaiting {
		a.waiting.Add(-1)
		return func() {}, false
	}
	defer a.waiting.Add(-1)
	timer := time.NewTimer(a.wait)
	defer timer.Stop()
	select {
	case a.slots <- struct{}{}:
		return a.leaver(), true
	case <-ctx.Done():
	case <-timer.C:
	}
	return func() {}, false
}

func (a *admission) leaver() func() {
	var once sync.Once
	return func() { once.Do(func() { <-a.slots }) }
}
