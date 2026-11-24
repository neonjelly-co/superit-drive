// Command notices writes THIRD-PARTY-NOTICES.txt: the copyright notices and licence texts of everything that is not
// the Drive's own and goes out with it. Run it from the repository's top folder, after the interface's packages are
// installed (`make notices` does both).
//
// What goes out with the Drive is found, not listed by hand:
//
//   - the Go modules compiled into cmd/superit-drive for the platforms the image is published for, from `go list`,
//     each with every licence, notice and patent file at its top;
//   - Go's own standard library and runtime, whose texts are kept in static/ (a Go installation does not always carry
//     them, and the file must not depend on which Go wrote it);
//   - the npm packages the interface's `dependencies` reach, which are the ones a browser is sent, each with its
//     licence files. What only builds the interface (devDependencies) is not in the Drive;
//   - the CA certificates the image carries, which are named in a paragraph here.
//
// The file is the same whoever writes it, so the test next to this one fails when a dependency changed and the file was
// not written again.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	_ "embed"
)

// File is the file written, in the repository's top folder.
const File = "THIRD-PARTY-NOTICES.txt"

// platforms are the ones the image is published for (.github/workflows/release.yml).
var platforms = [][2]string{{"linux", "amd64"}, {"linux", "arm64"}}

var (
	//go:embed static/go-LICENSE
	goLicense string
	//go:embed static/go-PATENTS
	goPatents string
)

const certificates = `The container image carries the CA certificates of the Alpine Linux package "ca-certificates"
(/etc/ssl/certs/ca-certificates.crt), which is Mozilla's list of certificate authorities. The list is under the
Mozilla Public License, version 2.0 (https://www.mozilla.org/MPL/2.0/), and is carried as Alpine Linux publishes it.
`

// A component is one piece of software that goes out with the Drive.
type component struct {
	name    string // "golang.org/x/sys v0.49.0"
	kind    string // "Go module", "npm package", …
	license string // what the package itself says its licence is, when it says
	texts   []text
}

type text struct{ name, body string }

func main() {
	out, err := generate(".")
	if err == nil {
		err = os.WriteFile(File, out, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "notices:", err)
		os.Exit(1)
	}
}

// generate returns the file's content for the repository at root.
func generate(root string) ([]byte, error) {
	mods, err := goModules(root)
	if err != nil {
		return nil, err
	}
	pkgs, err := npmPackages(filepath.Join(root, "web"))
	if err != nil {
		return nil, err
	}
	components := []component{{
		name: "Go standard library and runtime", kind: "compiled into the Drive",
		texts: []text{{"LICENSE", goLicense}, {"PATENTS", goPatents}},
	}}
	components = append(components, mods...)
	components = append(components, pkgs...)
	components = append(components, component{
		name: "CA certificates", kind: "in the container image", license: "MPL-2.0",
		texts: []text{{"", certificates}},
	})

	var b bytes.Buffer
	b.WriteString(`Third-party notices for Superit Drive
=====================================

Superit Drive is published by neonjelly studio under the GNU Affero General Public License, version 3 (the file
LICENSE). The software below is not the Drive's own: it is compiled into the Drive, sent to the browser as part of the
interface, or carried in the container image. Each keeps its own licence, and its notices follow in full.

This file is written by ` + "`make notices`" + ` (dev/notices). Do not change it by hand.

`)
	for _, c := range components {
		fmt.Fprintf(&b, "  %s (%s)\n", c.name, describe(c))
	}
	rule := strings.Repeat("=", 100)
	for _, c := range components {
		fmt.Fprintf(&b, "\n%s\n%s\n%s\n%s\n", rule, c.name, describe(c), rule)
		for _, t := range c.texts {
			if t.name != "" {
				fmt.Fprintf(&b, "\n---- %s ----\n", t.name)
			}
			b.WriteString("\n" + clean(t.body) + "\n")
		}
	}
	return b.Bytes(), nil
}

func describe(c component) string {
	if c.license == "" {
		return c.kind
	}
	return c.kind + ", " + c.license
}

// clean makes a text's line endings the same everywhere and takes the empty lines off its ends.
func clean(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// goModules are the modules, other than the Drive's own, with a package compiled into the Drive on any platform it is
// published for.
func goModules(root string) ([]component, error) {
	type module struct{ version, dir string }
	found := map[string]module{}
	for _, p := range platforms {
		cmd := exec.Command("go", "list", "-deps", "-f",
			`{{with .Module}}{{if not .Main}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{.Dir}}{{end}}{{end}}`,
			"./cmd/superit-drive")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS="+p[0], "GOARCH="+p[1], "CGO_ENABLED=0", "GOFLAGS=-mod=readonly", "GOWORK=off")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go list for %s/%s: %w\n%s", p[0], p[1], err, stderr.String())
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f := strings.Split(line, "\t"); len(f) == 3 {
				found[f[0]] = module{f[1], f[2]}
			}
		}
	}
	if len(found) == 0 {
		return nil, errors.New("go list named no module compiled into the Drive")
	}
	var all []component
	for path, m := range found {
		texts, err := licenceFiles(m.dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		all = append(all, component{name: path + " " + m.version, kind: "Go module, compiled into the Drive", texts: texts})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].name < all[j].name })
	return all, nil
}

// npmPackages are the packages the interface's dependencies reach, through their own dependencies: what the built
// interface holds. Each is found as Node finds it, in the nearest node_modules above the package that asks for it.
func npmPackages(web string) ([]component, error) {
	type manifest struct {
		Name                 string            `json:"name"`
		Version              string            `json:"version"`
		License              string            `json:"license"`
		Dependencies         map[string]string `json:"dependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	read := func(dir string) (manifest, error) {
		var m manifest
		raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
		if err != nil {
			return m, err
		}
		return m, json.Unmarshal(raw, &m)
	}
	find := func(from, name string) (string, bool) {
		for dir := from; ; dir = filepath.Dir(dir) {
			at := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
			if _, err := os.Stat(filepath.Join(at, "package.json")); err == nil {
				return at, true
			}
			if dir == web || dir == filepath.Dir(dir) {
				return "", false
			}
		}
	}

	top, err := read(web)
	if err != nil {
		return nil, err
	}
	type want struct {
		from, name string
		optional   bool
	}
	var queue []want
	for name := range top.Dependencies {
		queue = append(queue, want{web, name, false})
	}
	seen := map[string]bool{}
	var all []component
	for len(queue) > 0 {
		w := queue[0]
		queue = queue[1:]
		dir, ok := find(w.from, w.name)
		if !ok {
			if w.optional {
				continue
			}
			return nil, fmt.Errorf("the npm package %s is not installed: run `bun install --frozen-lockfile` in web/", w.name)
		}
		if seen[dir] {
			continue
		}
		seen[dir] = true
		m, err := read(dir)
		if err != nil {
			return nil, err
		}
		texts, err := licenceFiles(dir)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Name, err)
		}
		all = append(all, component{
			name: m.Name + " " + m.Version, kind: "npm package, part of the interface", license: m.License, texts: texts,
		})
		for name := range m.Dependencies {
			queue = append(queue, want{dir, name, false})
		}
		for name := range m.OptionalDependencies {
			queue = append(queue, want{dir, name, true})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].name < all[j].name })
	return all, nil
}

// licenceFiles are the licence, notice and patent files at the top of dir. A package with none is an error: its terms
// have to be read by a person before it goes out with the Drive.
func licenceFiles(dir string) ([]text, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var texts []text
	for _, e := range entries {
		name := strings.ToLower(e.Name())
		is := false
		for _, prefix := range []string{"license", "licence", "copying", "notice", "patents", "unlicense"} {
			is = is || strings.HasPrefix(name, prefix)
		}
		if !is || e.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		texts = append(texts, text{e.Name(), string(body)})
	}
	if len(texts) == 0 {
		return nil, errors.New("no licence file at its top")
	}
	sort.Slice(texts, func(i, j int) bool { return texts[i].name < texts[j].name })
	return texts, nil
}
