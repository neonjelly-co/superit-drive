package server

import (
	"testing"
	"time"
)

// A person has a fixed number of mails sent within a while: the next is refused until the oldest is a while old. One
// that was not sent after all does not count, each person's count is their own, and whoever caused none lately is
// forgotten.
func TestMailsAreCountedPerPersonWithinAWhile(t *testing.T) {
	c := newMailCount(3, time.Hour)
	start := time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)
	for i := range 3 {
		if !c.take("bence", start.Add(time.Duration(i)*10*time.Minute)) {
			t.Fatalf("mail %d of 3 was refused", i+1)
		}
	}
	if c.take("bence", start.Add(30*time.Minute)) || c.take("bence", start.Add(59*time.Minute)) {
		t.Fatal("a fourth mail within the hour was counted, want it refused")
	}
	if !c.take("reka", start.Add(30*time.Minute)) {
		t.Fatal("another person was refused for Bence's mails")
	}
	// The first is an hour old: one more, and only one.
	if !c.take("bence", start.Add(time.Hour)) || c.take("bence", start.Add(time.Hour+time.Minute)) {
		t.Fatal("an hour after the first mail: want one more counted, and the next refused")
	}
	// A mail that was not sent after all leaves room for another.
	c.giveBack("bence", start.Add(time.Hour))
	if !c.take("bence", start.Add(time.Hour+2*time.Minute)) {
		t.Fatal("refused after a mail was taken off the count")
	}
	c.giveBack("nobody", start)
	// Refusals count for nothing, and nothing is kept of those who caused no mail within the hour.
	for range 1000 {
		c.take("bence", start.Add(time.Hour+3*time.Minute))
	}
	if n := len(c.sent["bence"]); n != 3 {
		t.Fatalf("%d of Bence's mails are kept, want 3", n)
	}
	if !c.take("dora", start.Add(3*time.Hour)) {
		t.Fatal("refused hours later")
	}
	if len(c.sent) != 1 {
		t.Fatalf("%d people are kept hours after their last mail, want only the one who just caused one", len(c.sent))
	}
}

// A guest's address names a mail domain, never a machine by its number: a mail server that takes those would deliver
// to whichever machine a member picked.
func TestAGuestsAddressIsNeverAMachinesNumber(t *testing.T) {
	for address, want := range map[string]bool{
		"anna@client.example":           true,
		" Anna@Client.Example ":         true,
		"x@[10.0.0.5]":                  false,
		"x@[IPv6:::1]":                  false,
		"x@[::ffff:10.0.0.5]":           false,
		"x@10.0.0.5":                    false,
		"x@localhost":                   false,
		"x@client.example.":             false,
		"a b@client.example":            false,
		"anna\r\nBcc: x@client.example": false,
	} {
		if _, ok := guestAddress(address); ok != want {
			t.Errorf("%q as a guest's address: %v, want %v", address, ok, want)
		}
	}
}
