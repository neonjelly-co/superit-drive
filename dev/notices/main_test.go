package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The file in the repository is the one the dependencies as they are now give: a dependency added, removed or moved to
// another version without `make notices` fails here.
func TestTheNoticesAreCurrent(t *testing.T) {
	root := filepath.Join("..", "..")
	if _, err := os.Stat(filepath.Join(root, "web", "node_modules")); err != nil {
		t.Skip("the interface's packages are not installed (`make ui`), so its notices cannot be read")
	}
	want, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, File))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is not what the dependencies give now. Write it again with `make notices`.", File)
	}
}
