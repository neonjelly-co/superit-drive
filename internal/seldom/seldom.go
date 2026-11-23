// Package seldom logs the lines that anyone can cause: at a bounded rate, with what they quote cut short, so that
// nobody fills the Drive's log by asking again and again.
package seldom

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Each is how often a line that anyone can cause is logged, at most.
const Each = time.Minute

// Most is how many bytes of something the person sent (a path, an Origin, a name) a line quotes.
const Most = 200

// A Line is one kind of line in the log, written at most once in every interval. Those left out are counted, and the
// next one written says how many they were ("leftOut"). The count is in memory: a restart forgets it.
type Line struct {
	every time.Duration
	now   func() time.Time

	mu      sync.Mutex
	last    time.Time
	leftOut int64
}

// Every makes a line that is written at most once in every interval.
func Every(interval time.Duration) *Line { return &Line{every: interval, now: time.Now} }

// Log writes the line, unless one was written less than the interval ago: then it is only counted.
func (l *Line) Log(log *slog.Logger, level slog.Level, msg string, args ...any) {
	l.mu.Lock()
	now := l.now()
	if !l.last.IsZero() && now.Sub(l.last) < l.every {
		l.leftOut++
		l.mu.Unlock()
		return
	}
	leftOut := l.leftOut
	l.last, l.leftOut = now, 0
	l.mu.Unlock()
	if leftOut > 0 {
		args = append(args[:len(args):len(args)], "leftOut", leftOut)
	}
	log.Log(context.Background(), level, msg, args...)
}

// Cut is s as a line quotes it: its first Most bytes at most (never part of a character), then "…" when there was more.
func Cut(s string) string {
	if len(s) <= Most {
		return s
	}
	cut := s[:Most]
	for len(cut) > 0 && !utf8.RuneStart(s[len(cut)]) {
		cut = cut[:len(cut)-1]
	}
	return strings.ToValidUTF8(cut, "") + "…"
}
