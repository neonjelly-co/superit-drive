// Package text holds every text the Drive's server writes for a person to read (the answers its API gives, the pages
// visitors see, and its mail) in each language it speaks: English and Hungarian. Each text is one Message, made once
// when the program starts with both its languages side by side, so none can be missing; a request picks its language
// (Of) and the Message says itself in it. Nothing is loaded or built per request.
//
// What only the people running the Drive read (its log, its settings' errors, the storage check's output) stays in
// English and is not here.
//
// Hungarian is written as a Hungarian would write it, in the same plain and friendly way as the English (and as the
// platform's own Hungarian, which says "te"): not word by word. Where a text holds a name or a number, it is phrased so
// that the value needs no suffix and no article of its own ("Kukába került: „Számla.pdf”", never "a(z) %s-t"), since
// those change with the value's last and first sounds.
package text

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Lang is a language the Drive speaks to people in.
type Lang uint8

const (
	English Lang = iota
	Hungarian
)

// Langs is every language the Drive speaks, English first.
var Langs = []Lang{English, Hungarian}

// Code is the language's tag as HTML and HTTP write it: "en" or "hu".
func (l Lang) Code() string {
	if l == Hungarian {
		return "hu"
	}
	return "en"
}

// Parse is the language a tag names ("en", "hu", or "hu-HU" and the like, in any case); ok is false for any other.
func Parse(tag string) (l Lang, ok bool) {
	primary, _, _ := strings.Cut(strings.TrimSpace(tag), "-")
	switch strings.ToLower(primary) {
	case "en":
		return English, true
	case "hu":
		return Hungarian, true
	}
	return English, false
}

// Message is one text a person reads, in every language. The zero Message says nothing.
type Message struct{ in [2]string }

var (
	allMu sync.Mutex
	all   []Message
)

// M makes a message from its English and its Hungarian text. Both are fmt formats when the message is said with values,
// holding the same verbs (the Hungarian may number them, %[2]s, where its order differs). Messages are made once, as
// package variables: M also keeps each for the test that every one has its Hungarian (All).
func M(en, hu string) Message {
	m := Message{[2]string{en, hu}}
	allMu.Lock()
	all = append(all, m)
	allMu.Unlock()
	return m
}

// All is every message made with M so far: in a program, every message of every package it holds.
func All() []Message {
	allMu.Lock()
	defer allMu.Unlock()
	return append([]Message(nil), all...)
}

// In says the message in the language l, with values when it has verbs for them.
func (m Message) In(l Lang, values ...any) string {
	if int(l) >= len(m.in) {
		l = English
	}
	if len(values) == 0 {
		return m.in[l]
	}
	return fmt.Sprintf(m.in[l], values...)
}

// English says the message in English: for an error's own text, which only the log reads.
func (m Message) English(values ...any) string { return m.In(English, values...) }

// IsZero says whether the message is the zero Message.
func (m Message) IsZero() bool { return m == Message{} }

var verb = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?[a-zA-Z%]`)

// Problem says what is wrong with a message, or "" when nothing is: a language missing, the Hungarian the same as the
// English (left untranslated), or the two holding different verbs (a value said in one and not the other).
func (m Message) Problem() string {
	en, hu := m.in[English], m.in[Hungarian]
	switch {
	case strings.TrimSpace(en) == "":
		return fmt.Sprintf("the message %q has no English", hu)
	case strings.TrimSpace(hu) == "":
		return fmt.Sprintf("%q has no Hungarian", en)
	case en == hu:
		return fmt.Sprintf("%q is the same in Hungarian: it was left untranslated", en)
	}
	if a, b := verbsOf(en), verbsOf(hu); a != b {
		return fmt.Sprintf("%q and its Hungarian %q do not say the same values (%s, %s)", en, hu, a, b)
	}
	return ""
}

// verbsOf is the verbs a format holds, in the order of the values they say, with their numbers taken out: "%s %d" and
// "%[2]d %[1]s" both hold "s d".
func verbsOf(format string) string {
	type said struct {
		at   int
		verb string
	}
	var out []said
	next := 1
	for _, v := range verb.FindAllString(format, -1) {
		if v == "%%" {
			continue
		}
		at := next
		if i := strings.Index(v, "["); i >= 0 {
			at, _ = strconv.Atoi(v[i+1 : strings.Index(v, "]")])
		}
		next = at + 1
		out = append(out, said{at, v[len(v)-1:]})
	}
	byAt := map[int]string{}
	most := 0
	for _, s := range out {
		byAt[s.at] = s.verb
		most = max(most, s.at)
	}
	parts := make([]string, 0, most)
	for i := 1; i <= most; i++ {
		parts = append(parts, byAt[i])
	}
	return strings.Join(parts, " ")
}

// FromHeader is the language for a browser's Accept-Language header: of the languages the Drive speaks, the one the
// browser prefers most (by its weight, then by its place in the list); English when it names neither.
func FromHeader(header string) Lang {
	best, bestQ, found := English, -1.0, false
	for part := range strings.SplitSeq(header, ",") {
		tag, params, _ := strings.Cut(part, ";")
		l, ok := Parse(tag)
		if !ok {
			continue
		}
		q := 1.0
		for p := range strings.SplitSeq(params, ";") {
			if k, v, ok := strings.Cut(strings.TrimSpace(p), "="); ok && strings.EqualFold(k, "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		if q > 0 && q > bestQ {
			best, bestQ, found = l, q, true
		}
	}
	if !found {
		return English
	}
	return best
}

// Of is the language to answer a request in: the one its "lang" query parameter names ("en" or "hu"), which the
// visitors' pages carry once a visitor chose one; otherwise the one its Accept-Language header prefers, which the
// interface sets to the language the person chose (and a browser to its own).
func Of(r *http.Request) Lang {
	if l, ok := Chosen(r); ok {
		return l
	}
	return FromHeader(r.Header.Get("Accept-Language"))
}

// Chosen is the language a request's "lang" query parameter names, if it names one.
func Chosen(r *http.Request) (Lang, bool) {
	if !strings.Contains(r.URL.RawQuery, "lang=") {
		return English, false
	}
	v := r.URL.Query().Get("lang")
	if v != "en" && v != "hu" {
		return English, false
	}
	return Parse(v)
}

// Size is a file's size as people read it, the way storage is sold (1 KB is 1000 bytes), with the language's decimal
// mark: "84 KB", "1.2 MB", in Hungarian "1,2 MB". The members' interface writes it the same way.
func Size(l Lang, bytes int64) string {
	if bytes < 1000 {
		return strconv.FormatInt(bytes, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value, unit := float64(bytes)/1000, 0
	for unit < len(units)-1 && value+0.5 >= 1000 {
		value /= 1000
		unit++
	}
	shown := strconv.FormatFloat(value, 'f', 0, 64)
	if value < 9.95 {
		shown = strconv.FormatFloat(value, 'f', 1, 64)
		if l == Hungarian {
			shown = strings.Replace(shown, ".", ",", 1)
		}
	}
	return shown + " " + units[unit]
}

var months = [2][12]string{
	{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
	{"január", "február", "március", "április", "május", "június", "július", "augusztus", "szeptember", "október", "november", "december"},
}

// Moment is a day and time as people read it in mail, in UTC (the Drive does not know the reader's time zone, and
// says so): "2 January 2026, 15:04 UTC", in Hungarian "2026. január 2. 15:04 (UTC)".
func Moment(l Lang, t time.Time) string {
	t = t.UTC()
	if l == Hungarian {
		return fmt.Sprintf("%d. %s %d. %s (UTC)", t.Year(), months[Hungarian][t.Month()-1], t.Day(), t.Format("15:04"))
	}
	return fmt.Sprintf("%d %s %d, %s UTC", t.Day(), months[English][t.Month()-1], t.Year(), t.Format("15:04"))
}
