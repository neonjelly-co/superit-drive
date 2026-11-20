package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/neonjelly-co/superit-drive/internal/access"
	"github.com/neonjelly-co/superit-drive/internal/drive"
	"github.com/neonjelly-co/superit-drive/internal/index"
	"github.com/neonjelly-co/superit-drive/internal/text"
)

// Sending files in through an upload-only public link: its page, and the one route under the public prefix that
// changes anything. guardVisitor has decided (access.UploadThroughLink) before either runs: the link's own folder, never
// anything inside it.
//
// The page is the link's own (GET /s/{token}): who shared it, the folder's name, and a place to choose or drop files,
// with a line that says the folder's contents cannot be seen. Nothing of what is in the folder is on it. A small script
// in the page (sendScript) sends each file as one request with its length, one after another, shows how far each has
// gone, and sends one again when the Drive says it is not its turn. It is the page's only script, allowed by its hash,
// and may talk only to the public prefix of the Drive's own address (sendPolicy); the page sends no form anywhere.
// Without JavaScript the page says that sending needs it.
//
// Why a script and not a plain form: a form posts every file chosen in one request, as multipart, in which no file says
// how long it is before it has all arrived. The Drive could then neither keep a file's length against the link's limit
// before reading it, nor stream it to the storage with its length (the one PUT that writes the whole file or nothing),
// and one large file cut off would lose all the others with it. One request per file, its length in Content-Length, is
// the shape a member's upload in one request already has, bounded the same way.

// errNoLongerThroughLink and errSwitchedOffNow say why a file that arrived whole was not kept: the link no longer lets
// files in there (removed, made a view link, its folder in the trash or gone, its maker no longer able to share it), or
// sharing outside the project was switched off there meanwhile.
var (
	errNoLongerThroughLink = errors.New("the link no longer lets files in there")
	errSwitchedOffNow      = errors.New("sharing outside the project was switched off there")
)

// sendInPage is an upload-only link's page: who shared it, the folder's name, and a place to choose or drop files and
// send them. It says that what is in the folder cannot be seen, and shows none of it. Once the link has taken in all it
// may, it says so instead of offering to send, and has no script.
func (s *Server) sendInPage(w http.ResponseWriter, r *http.Request, c call) {
	full := c.link.TakenIn >= s.drive.LinkLimit()
	s.renderVisitor(w, r, http.StatusOK, visitorView{Name: c.item.Name, By: s.sharer(*c.link, text.Of(r)), Folder: true, Send: true, Full: full})
	s.used(r, c)
}

// sendIn keeps a file a visitor sends through an upload-only link, in the folder the link is on, and answers 201 with
// its size: nothing else, and the same whether its name was free there or not. A name that is taken (by a file, a
// folder, or a change on its way) is never replaced: the file is kept beside it under the first free name, " (2)" and
// so on, and nobody is told. The file is labelled as come through a link, and counts against what the link may take in
// (drive.LinkLimit) from before its first byte is read.
//
//	POST /s/{token}/files?name=<the file's name>[&turn=<word>]
//	Content-Length: <the file's size>
//
//	<the file's bytes>
//
//	201 {"size": n}
//	400 {"error"}                                 a name the Drive does not store, or the file stopped before its end
//	411 {"error"}                                 no Content-Length
//	413 {"error", "full": true | false}           it would take the link past its limit (full: it has reached it)
//	503 {"error", "busy": true}, Retry-After      not its turn: send it again then, with the same turn
//	404, 403                                      the pages for a link that does not work, or is not available now
//
// The link is decided on again once the file is whole, holding its place: one removed, made a view link, or no longer
// counting meanwhile keeps nothing, and answers as such a link does.
func (s *Server) sendIn(w http.ResponseWriter, r *http.Request, c call) {
	if r.ContentLength < 0 {
		refuseWith(w, r, http.StatusLengthRequired, nil, sendNoLength)
		return
	}
	name := r.URL.Query().Get("name")
	var bad *drive.BadName
	if err := drive.CheckSentName(name); errors.As(err, &bad) {
		refuseWith(w, r, http.StatusBadRequest, nil, bad.Why)
		return
	}
	release, ok := s.tryTurn(w, r, c, turnKey(r, c))
	if !ok {
		return
	}
	defer release()
	body, done := receiving(w, r)
	defer done()
	_, err := s.drive.SendIn(r.Context(), *c.place, c.link.ID, name, body, r.ContentLength,
		func(ctx context.Context) (index.Place, index.Item, error) { return s.stillSendingIn(ctx, c) })
	switch {
	case err == nil:
		s.used(r, c)
		writeJSON(w, http.StatusCreated, map[string]int64{"size": r.ContentLength})
	case errors.Is(err, drive.ErrLinkFull):
		full, ferr := s.linkIsFull(r.Context(), c.link.ID)
		if ferr != nil {
			s.fail(w, r, ferr)
			return
		}
		message := linkTooSmall
		if full {
			message = linkFull
		}
		refuseWith(w, r, http.StatusRequestEntityTooLarge, map[string]any{"full": full}, message)
	case errors.Is(err, drive.ErrCutOff) || r.Context().Err() != nil:
		// The visitor's connection ended (or they stopped it); nothing was kept. Not the Drive's failure, and not logged:
		// anyone with the address could fill the log by hanging up, as anyone can try tokens without a log line.
		refuseWith(w, r, http.StatusBadRequest, nil, sendCutOff)
	case errors.Is(err, errSwitchedOffNow):
		s.switchedOff(w, r, c)
	case errors.Is(err, errNoLongerThroughLink), errors.Is(err, index.ErrNoItem), errors.Is(err, index.ErrNoLink),
		errors.Is(err, drive.ErrPlaceGone):
		s.refuse(w, r, http.StatusNotFound, notHere)
	case errors.As(err, &bad):
		refuseWith(w, r, http.StatusBadRequest, nil, bad.Why)
	case errors.Is(err, drive.ErrGone), errors.As(err, new(*drive.Taken)):
		// Its folder moved or went from the storage meanwhile; or (after hundreds of files of one name) no free name.
		refuseWith(w, r, http.StatusConflict, nil, sendFolderNow)
	case errors.Is(err, drive.ErrFull):
		refuseWith(w, r, http.StatusInsufficientStorage, nil, sendNoRoom)
	default:
		s.failed(w, r, err)
	}
}

// stillSendingIn decides on the link a file is sent through again, once the file is whole and its place is held: the
// link must still be the one the visitor came with (a link made in its place has another id), upload-only, and still
// let files in at its folder, in the same place. Everything is read again, the place too: whether sharing outside the
// project is switched off there, and whether it is held, are read with it. It returns the place and the folder as they
// are now.
func (s *Server) stillSendingIn(ctx context.Context, c call) (index.Place, index.Item, error) {
	l, err := s.drive.LinkByID(ctx, c.link.ID)
	if errors.Is(err, index.ErrNoLink) {
		return index.Place{}, index.Item{}, errNoLongerThroughLink
	}
	if err != nil {
		return index.Place{}, index.Item{}, err
	}
	if l.Place != c.place.ID {
		return index.Place{}, index.Item{}, drive.ErrGone // its folder moved into another place meanwhile: send it again
	}
	p, err := s.drive.Place(ctx, l.Place)
	if errors.Is(err, index.ErrNoPlace) {
		return index.Place{}, index.Item{}, errNoLongerThroughLink // a shared folder in the trash
	}
	if err != nil {
		return index.Place{}, index.Item{}, err
	}
	it, err := s.drive.Item(ctx, p, l.Item)
	if errors.Is(err, index.ErrNoItem) {
		return index.Place{}, index.Item{}, errNoLongerThroughLink
	}
	if err != nil {
		return index.Place{}, index.Item{}, err
	}
	where, err := s.accessAt(ctx, p, idsOf(pathTo(it)))
	if err != nil {
		return index.Place{}, index.Item{}, err
	}
	who := visitorOf(l, s.members.Current())
	if !access.Decide(who, access.UploadThroughLink, where) {
		if access.SwitchedOff(who, access.UploadThroughLink, where) {
			return index.Place{}, index.Item{}, errSwitchedOffNow
		}
		return index.Place{}, index.Item{}, errNoLongerThroughLink
	}
	return p, it, nil
}

// linkIsFull says whether the link with id has taken in all it may until it is reset.
func (s *Server) linkIsFull(ctx context.Context, id int64) (bool, error) {
	l, err := s.drive.LinkByID(ctx, id)
	if errors.Is(err, index.ErrNoLink) {
		return true, nil
	}
	return l.TakenIn >= s.drive.LinkLimit(), err
}

// sendPolicyFor is the Content-Security-Policy of an upload-only link's page, for a Drive whose own address has the
// origin given: as visitorPolicy, and the page's one script, by its hash, which may talk to the public prefix of that
// address and nowhere else (to wherever the page came from, when the address is not known).
func sendPolicyFor(origin string) string {
	connect := "'self'"
	if origin != "" {
		connect = origin + PublicPrefix
	}
	return fmt.Sprintf("default-src 'none'; style-src '%s'; script-src '%s'; connect-src %s; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
		styleHash(visitorStyle), styleHash(sendScript), connect)
}

// sendScript is an upload-only link's page's script: it takes the files chosen (the file field) or dropped (on the
// place for it, without folders, which cannot be sent), and sends each one, one after another, as the body of a POST to
// the page's own address + "/files", with its name (in Unicode's composed form, as the members' interface sends names),
// a turn of its own, and the page's language (so the Drive's answers are in it too). It shows how far each has gone;
// one the Drive says is not its turn waits Retry-After and goes again with the same turn; one whose connection is lost
// goes again from its start, with the same turn, for a few minutes. A link that no longer works, is not available or
// is full stops them all, with the Drive's words.
//
// Everything it says is in the page, in the page's language (data-words on #sending, sendWordsOf): the script holds no
// words, so that one script, and one hash in the policy, serves every language.
const sendScript = `(function () {
"use strict";
var input = document.getElementById("files"), drop = document.getElementById("drop"), list = document.getElementById("chosen"),
	go = document.getElementById("send"), said = document.getElementById("said"),
	words = JSON.parse(document.getElementById("sending").getAttribute("data-words")),
	lang = document.documentElement.lang;
var to = location.pathname.replace(/\/+$/, "") + "/files", files = [], busy = false;
var stops = {404: words.gone, 403: words.off};
function size(n) {
	if (n < 1000) return n + " B";
	var units = ["KB", "MB", "GB", "TB"], v = n / 1000, u = 0;
	while (u < units.length - 1 && v + 0.5 >= 1000) { v /= 1000; u++; }
	return (v < 9.95 ? v.toFixed(1).replace(".", words.decimal) : v.toFixed(0)) + " " + units[u];
}
function turn() {
	var b = new Uint8Array(12), t = "";
	crypto.getRandomValues(b);
	for (var i = 0; i < b.length; i++) t += ("0" + b[i].toString(16)).slice(-2);
	return t;
}
function left() { return files.filter(function (f) { return !f.sent; }).length; }
function ready() {
	var n = left();
	go.disabled = busy || n === 0;
	go.textContent = n > 1 ? words.sendN.replace("{n}", n) : words.send;
}
function show(f, text, kind) { f.state.textContent = text; f.state.className = "state" + (kind ? " " + kind : ""); }
function add(chosen) {
	for (var i = 0; i < chosen.length; i++) {
		var f = {file: chosen[i], turn: turn(), sent: false, failed: false, tries: 0}, row = document.createElement("li"),
			name = document.createElement("span");
		name.className = "name";
		name.textContent = f.file.name;
		f.state = document.createElement("span");
		row.appendChild(name);
		row.appendChild(f.state);
		list.appendChild(row);
		show(f, size(f.file.size), "");
		files.push(f);
	}
	list.hidden = files.length === 0;
	said.textContent = "";
	ready();
}
input.addEventListener("change", function () { add(input.files); input.value = ""; });
drop.addEventListener("dragover", function (e) { e.preventDefault(); drop.classList.add("over"); });
drop.addEventListener("dragleave", function () { drop.classList.remove("over"); });
drop.addEventListener("drop", function (e) {
	e.preventDefault();
	drop.classList.remove("over");
	var chosen = [], folders = 0, items = e.dataTransfer.items, all = e.dataTransfer.files;
	for (var i = 0; i < all.length; i++) {
		var entry = items && items[i] && items[i].webkitGetAsEntry ? items[i].webkitGetAsEntry() : null;
		if (entry && entry.isDirectory) folders++;
		else chosen.push(all[i]);
	}
	add(chosen);
	if (folders) said.textContent = words.folders;
});
go.addEventListener("click", function () { busy = true; said.textContent = ""; ready(); next(); });
function next() {
	for (var i = 0; i < files.length; i++) if (!files[i].sent && !files[i].failed) { send(files[i]); return; }
	finish("");
}
function finish(why) {
	var sent = files.filter(function (f) { return f.sent; }).length, n = left();
	files.forEach(function (f) { f.failed = false; f.tries = 0; });
	busy = false;
	said.textContent = why || (n === 0 ? (sent === 1 ? words.sentOne : words.sentAll.replace("{n}", sent)) :
		(n === 1 ? words.failedOne : words.failedN.replace("{n}", n)) + " " + words.again);
	ready();
}
function send(f) {
	var x = new XMLHttpRequest();
	x.open("POST", to + "?name=" + encodeURIComponent(f.file.name.normalize("NFC")) + "&turn=" + f.turn + "&lang=" + lang);
	x.upload.onprogress = function (e) {
		if (e.lengthComputable && e.total > 0) show(f, Math.floor(e.loaded * 100 / e.total) + "%", "");
	};
	x.onload = function () {
		var answer = {};
		try { answer = JSON.parse(x.responseText); } catch (e) {}
		if (x.status === 201) { f.sent = true; show(f, words.sent, "done"); next(); return; }
		if (x.status === 503) {
			var after = parseInt(x.getResponseHeader("Retry-After"), 10);
			show(f, words.waiting, "");
			setTimeout(function () { send(f); }, (after > 0 ? after : 2) * 1000);
			return;
		}
		var why = answer.error || stops[x.status] || words.failed;
		if (stops[x.status] || answer.full || x.status === 507) { show(f, words.notSent, "bad"); finish(why); return; }
		f.failed = true;
		show(f, why, "bad");
		next();
	};
	x.onerror = function () {
		// A connection lost, or the Drive's answer that it was not this file's turn arriving as one: again, with the same
		// turn, often enough to keep its place in line (the Drive forgets a turn not asked about for a minute).
		if (++f.tries <= 20) {
			show(f, words.retrying, "");
			setTimeout(function () { send(f); }, Math.min(2000 * f.tries, 15000));
			return;
		}
		f.failed = true;
		show(f, words.stopped, "bad");
		next();
	};
	x.send(f.file);
}
})();
`
