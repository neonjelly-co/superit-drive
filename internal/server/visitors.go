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

const (
	// visitorNotHere is the one answer for a link that does not work, and for anything a link does not cover.
	visitorNotHere = "This link does not work. It may have been removed, or its address may be incomplete. Ask whoever sent it to you for a new one."
	onlyReading    = "A public link only opens and downloads: nothing can be changed through one."
	visitorsBusy   = "The Drive is busy. Try again in a moment."
)

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
	defer leave()
	c, ok := s.throughLink(w, r, rt)
	if !ok {
		return
	}
	if !access.Decide(c.person, rt.action, c.access) || rt.serve == nil {
		if rt.serve != nil && access.SwitchedOff(c.person, rt.action, c.access) {
			// The token matched, and the link would open this: it is not "not here", only not now.
			s.switchedOff(w, r, c)
			return
		}
		s.refuse(w, r, http.StatusNotFound, notHere)
		return
	}
	if rt.action == access.DownloadThroughLink || rt.action == access.UploadThroughLink {
		leave() // it waits for its transfer slot in the outsiders' own line, which bounds them by itself
	}
	rt.serve(w, r, c)
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
	path := pathTo(*c.item)
	at := slices.IndexFunc(path, func(it index.Item) bool { return it.ID == c.link.Item })
	if at < 0 {
		s.refuse(w, r, http.StatusNotFound, notHere) // access decided it is inside; never
		return
	}
	here := func(id int64) string {
		if id == c.link.Item {
			return PublicPrefix + token
		}
		return PublicPrefix + token + "/items/" + strconv.FormatInt(id, 10)
	}
	v := visitorView{Name: c.item.Name, By: s.sharer(*c.link), Folder: c.item.Folder}
	for _, f := range path[at : len(path)-1] {
		v.Crumbs = append(v.Crumbs, visitorLink{Name: f.Name, Href: here(f.ID)})
	}
	if c.item.Folder {
		items, more, err := s.drive.FirstChildren(r.Context(), *c.place, c.item, visitorListLimit)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v.Zip = PublicPrefix + token + "/items/" + strconv.FormatInt(c.item.ID, 10) + "/zip"
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
				row.Size = formatSize(it.Size)
				row.Href = PublicPrefix + token + "/items/" + strconv.FormatInt(it.ID, 10) + "/download"
				if opensInTheBrowser(it.Name) {
					row.Open = PublicPrefix + token + "/items/" + strconv.FormatInt(it.ID, 10) + "/open"
				}
			}
			v.Items = append(v.Items, row)
		}
		v.More = more
	} else {
		v.Size = formatSize(c.item.Size)
		v.Download = PublicPrefix + token + "/items/" + strconv.FormatInt(c.item.ID, 10) + "/download"
		if opensInTheBrowser(c.item.Name) {
			v.Open = PublicPrefix + token + "/items/" + strconv.FormatInt(c.item.ID, 10) + "/open"
		}
	}
	s.renderVisitor(w, http.StatusOK, v)
	s.used(r, c)
}

// sharer says who shared what a link is on, as a visitor reads it: the display name of the member who made it, and
// never their email address or user id. Without a name (they have none, or have left the project), the project's.
func (s *Server) sharer(l index.Link) string {
	if m, ok := s.members.Current().Lookup(l.MadeBy); ok && strings.TrimSpace(m.DisplayName) != "" {
		return "Shared by " + strings.TrimSpace(m.DisplayName)
	}
	return "Shared from " + s.project
}

// visitorRefusal answers a visitor with a page saying why. Every "not here" is the one page for a link that does not
// work, whatever it was about.
func (s *Server) visitorRefusal(w http.ResponseWriter, status int, message string) {
	v := visitorView{Heading: "This did not work", Message: message}
	if message == notHere {
		v = visitorView{Heading: "This link does not work", Message: visitorNotHere}
	}
	s.renderVisitor(w, status, v)
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
	// For a folder: the zip of it, what is in it, and whether there is more than is listed.
	Zip   string
	Items []visitorRow
	More  bool
	// For a file: its size, and where it opens (when its kind does) and downloads.
	Size, Open, Download string
	// Send is true for an upload-only link's page: a place to choose or drop files and send them into the folder, and
	// nothing of what is in it.
	Send bool
}

type visitorLink struct{ Name, Href string }

// visitorRow is one item of a folder on a visitor's page: a folder's Href is its page, a file's its download.
type visitorRow struct {
	Name       string
	Folder     bool
	Href       string
	Size, Open string
}

func (s *Server) renderVisitor(w http.ResponseWriter, status int, v visitorView) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	if v.Send {
		h.Set("Content-Security-Policy", s.sendPolicy)
	}
	w.WriteHeader(status)
	if err := visitorTemplate.Execute(w, struct {
		visitorView
		Style  template.CSS
		Script template.JS
		Title  string
	}{v, template.CSS(visitorStyle), template.JS(sendScript), titleOf(v)}); err != nil {
		s.log.Info("a visitor's page was not sent whole", "error", err)
	}
}

func titleOf(v visitorView) string {
	if v.Heading != "" {
		return v.Heading
	}
	return v.Name
}

// formatSize is a file's size the way the members' interface writes it, and storage is sold: 1 KB is 1000 bytes.
func formatSize(bytes int64) string {
	if bytes < 1000 {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value, unit := float64(bytes)/1000, 0
	for unit < len(units)-1 && value+0.5 >= 1000 {
		value /= 1000
		unit++
	}
	if value < 9.95 {
		return strconv.FormatFloat(value, 'f', 1, 64) + " " + units[unit]
	}
	return strconv.FormatFloat(value, 'f', 0, 64) + " " + units[unit]
}

// visitorStyle is the visitors' pages' only style, in the page itself: Pilot light's mist ground, white panel and
// graphite ink, in one column on every screen.
const visitorStyle = `:root{color-scheme:light;--background:oklch(0.956 0.006 239.8);--foreground:oklch(0.283 0.026 246.5);` +
	`--card:oklch(1 0 0);--muted:oklch(0.937 0.008 241.7);--muted-foreground:oklch(0.511 0.028 248.4);` +
	`--border:oklch(0.893 0.013 244.3);--ring:oklch(0.511 0.028 248.4);--destructive:oklch(0.544 0.186 26)}` +
	`*{box-sizing:border-box}` +
	`html,body{margin:0;background:var(--background);color:var(--foreground);font-family:"Atkinson Hyperlegible Next",` +
	`"Atkinson Hyperlegible",ui-sans-serif,system-ui,sans-serif;line-height:1.5;-webkit-text-size-adjust:100%}` +
	`body{min-height:100vh;display:flex;flex-direction:column}` +
	`main{width:100%;max-width:44rem;margin:0 auto;padding:2.5rem 1rem 2rem;flex:1}` +
	`.by{margin:0;color:var(--muted-foreground)}` +
	`.crumbs{margin:.75rem 0 0;color:var(--muted-foreground);overflow-wrap:anywhere}` +
	`.crumbs a{color:inherit}` +
	`h1{margin:.25rem 0 0;font-size:1.625rem;line-height:1.25;font-weight:700;overflow-wrap:anywhere}` +
	`.actions{display:flex;flex-wrap:wrap;gap:.5rem;margin:1.25rem 0 0}` +
	`.button{display:inline-flex;align-items:center;min-height:2.5rem;padding:.4rem 1rem;border-radius:.625rem;` +
	`background:var(--foreground);color:#fff;text-decoration:none;font-weight:600}` +
	`.button.quiet{background:var(--card);color:var(--foreground);border:1px solid var(--border)}` +
	`a:focus-visible{outline:3px solid var(--ring);outline-offset:2px}` +
	`.list{list-style:none;margin:1.5rem 0 0;padding:0;background:var(--card);border:1px solid var(--border);` +
	`border-radius:.75rem;overflow:hidden}` +
	`.list li{display:flex;flex-wrap:wrap;align-items:center;gap:.25rem .75rem;padding:.75rem 1rem;` +
	`border-top:1px solid var(--border)}` +
	`.list li:first-child{border-top:0}` +
	`.name{flex:1 1 12rem;min-width:0;display:flex;gap:.625rem;align-items:center;overflow-wrap:anywhere}` +
	`a.name{color:inherit;text-decoration:none;font-weight:600}a.name:hover{text-decoration:underline}` +
	`.icon{width:1.25rem;height:1.25rem;flex:none;color:var(--muted-foreground)}` +
	`.size{color:var(--muted-foreground);font-variant-numeric:tabular-nums;white-space:nowrap}` +
	`.do{display:flex;gap:1rem;white-space:nowrap}.do a{color:var(--foreground);font-weight:600}` +
	`.note{margin:1rem 0 0;color:var(--muted-foreground)}` +
	`.send{margin:1.5rem 0 0}` +
	`.drop{position:relative;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:.375rem;` +
	`min-height:10rem;padding:1.5rem 1rem;border:2px dashed var(--border);border-radius:.75rem;background:var(--card);` +
	`text-align:center}` +
	`.drop:hover,.drop.over{border-color:var(--ring)}` +
	`.drop:focus-within{outline:3px solid var(--ring);outline-offset:2px}` +
	`.drop input{position:absolute;inset:0;width:100%;height:100%;opacity:0;cursor:pointer}` +
	`.drop strong{font-weight:600}.drop span{color:var(--muted-foreground)}` +
	`.send .list .state{color:var(--muted-foreground);font-variant-numeric:tabular-nums;overflow-wrap:anywhere}` +
	`.send .list .state.bad{color:var(--destructive)}` +
	`button.button{border:0;font:inherit;cursor:pointer}button.button:disabled{opacity:.5;cursor:default}` +
	`button:focus-visible{outline:3px solid var(--ring);outline-offset:2px}` +
	`footer{padding:1.5rem 1rem;text-align:center;font-size:.875rem;color:var(--muted-foreground)}`

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

var visitorTemplate = template.Must(template.New("visitor").Funcs(template.FuncMap{
	"folderIcon": func() template.HTML { return folderIcon },
	"fileIcon":   func() template.HTML { return fileIcon },
}).Parse(`<!doctype html>
<html lang="en">
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
<nav class="crumbs" aria-label="Folders">{{range .Crumbs}}<a href="{{.Href}}">{{.Name}}</a> › {{end}}</nav>
{{- end}}
<h1>{{.Name}}</h1>
{{- if .Send}}
<p class="note">Send files to this folder. What is in it stays hidden: you cannot see its files, their names, or what anyone else sent. Nothing you send replaces anything in it.</p>
<div class="send">
<label class="drop" id="drop"><input type="file" id="files" multiple><strong>Drop files here, or choose them</strong><span>Files only: send what is in a folder, not the folder.</span></label>
<ul class="list" id="chosen" hidden></ul>
<p class="actions"><button class="button" id="send" type="button" disabled>Send</button></p>
<p class="note" id="said" role="status" aria-live="polite"></p>
</div>
<noscript><p class="note">Sending files needs JavaScript, which is switched off in this browser. Switch it on, then reload this page.</p></noscript>
<script>{{.Script}}</script>
{{- else if .Folder}}
<p class="actions"><a class="button" href="{{.Zip}}" download>Download all as zip</a></p>
{{- if .Items}}
<ul class="list">
{{- range .Items}}
{{- if .Folder}}
<li><a class="name" href="{{.Href}}">{{folderIcon}}<span>{{.Name}}</span></a></li>
{{- else}}
<li><span class="name">{{fileIcon}}<span>{{.Name}}</span></span><span class="size">{{.Size}}</span><span class="do">{{if .Open}}<a href="{{.Open}}" target="_blank" rel="noopener">Open</a>{{end}}<a href="{{.Href}}" download>Download</a></span></li>
{{- end}}
{{- end}}
</ul>
{{- if .More}}
<p class="note">Only the first {{len .Items}} items are listed here. Download all as zip to get everything.</p>
{{- end}}
{{- else}}
<p class="note">This folder is empty.</p>
{{- end}}
{{- else}}
<p class="note">{{.Size}}</p>
<p class="actions">{{if .Open}}<a class="button quiet" href="{{.Open}}" target="_blank" rel="noopener">Open</a>{{end}}<a class="button" href="{{.Download}}" download>Download</a></p>
{{- end}}
{{- end}}
</main>
<footer>Superit Drive</footer>
</body>
</html>
`))

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
