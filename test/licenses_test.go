package test

import (
	"strings"
	"testing"

	"github.com/neonjelly-co/superit-drive/test/harness"
)

// A copy of the Drive carries its licence and the notices of the software built into it: the binary prints them, with
// no setting at all.
func TestTheDrivePrintsItsLicences(t *testing.T) {
	t.Parallel()
	out, code := harness.Command(t, nil, "licenses")
	if code != 0 {
		t.Fatalf("exit code %d, want 0:\n%.2000s", code, out)
	}
	for _, want := range []string{
		"GNU AFFERO GENERAL PUBLIC LICENSE",
		"Third-party notices for Superit Drive",
		"modernc.org/sqlite", "Copyright 2009 The Go Authors.",
		"react-dom", "Copyright (c) Meta Platforms, Inc. and affiliates.",
		"SIL Open Font License",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the licences printed do not hold %q", want)
		}
	}
}
