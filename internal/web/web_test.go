package web

import (
	"io/fs"
	"mime"
	"path"
	"regexp"
	"strings"
	"testing"
)

var importRe = regexp.MustCompile(`(?:from|import)\s*["']([^"']+)["']`)

// With no build step, a mistyped import path only fails in the browser; this
// walks every embedded module and checks each relative import exists.
func TestEveryModuleImportResolves(t *testing.T) {
	root := FS()
	n := 0
	err := fs.WalkDir(root, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, ".js") || strings.HasSuffix(p, ".mjs")) {
			return err
		}
		src, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		for _, m := range importRe.FindAllStringSubmatch(string(src), -1) {
			spec := m[1]
			if !strings.HasPrefix(spec, ".") {
				t.Errorf("%s imports %q: only relative imports work without an import map", p, spec)
				continue
			}
			target := path.Clean(path.Join(path.Dir(p), spec))
			if _, err := fs.Stat(root, target); err != nil {
				t.Errorf("%s imports %q, which is not embedded", p, spec)
			}
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n < 10 {
		t.Fatalf("found only %d imports; is the UI embedded?", n)
	}
}

func TestEntryPointsAreEmbedded(t *testing.T) {
	for _, p := range []string{"index.html", "style.css", "js/main.js", "vendor/preact.mjs", "fonts/inter-latin-wght-normal.woff2"} {
		if _, err := fs.Stat(FS(), p); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}
	// Browsers refuse a module served with a non-JavaScript type.
	if ct := mime.TypeByExtension(".mjs"); !strings.Contains(ct, "javascript") {
		t.Errorf(".mjs is served as %q", ct)
	}
}
