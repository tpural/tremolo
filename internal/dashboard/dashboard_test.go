package dashboard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinsAreValid(t *testing.T) {
	s, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) == 0 {
		t.Fatal("no built-in dashboards")
	}
}

func TestDirOverridesBuiltin(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("errors.yaml", "id: errors\ntitle: Mine\npanels: [{title: x, query: up}]\n")
	write("extra.yml", "id: extra\ntitle: Extra\ngroup: Services\npanels: [{title: x, query: up, type: stat}]\n")
	write("notes.txt", "ignored")
	// ConfigMap volumes keep files in hidden directories next to symlinks.
	if err := os.Mkdir(filepath.Join(dir, "..data"), 0o700); err != nil {
		t.Fatal(err)
	}
	write("..data/hidden.yaml", "not: valid")

	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := s.Get("errors"); d.Title != "Mine" {
		t.Fatalf("errors = %+v", d)
	}
	d, ok := s.Get("extra")
	if !ok || d.Panels[0].Width != 6 {
		t.Fatalf("extra = %+v", d)
	}
}

func TestParseErrors(t *testing.T) {
	tests := map[string]string{
		"id and title":      "panels: [{title: x, query: up}]",
		"no panels":         "id: a\ntitle: A",
		"type must be":      "id: a\ntitle: A\npanels: [{title: x, query: up, type: pie}]",
		"no query":          "id: a\ntitle: A\npanels: [{title: x}]",
		"width must be":     "id: a\ntitle: A\npanels: [{title: x, query: up, width: 13}]",
		`unknown field "q"`: "id: a\ntitle: A\npanels: [{title: x, q: up}]",
	}
	for want, body := range tests {
		if _, err := Parse([]byte(body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) = %v, want %q", body, err, want)
		}
	}
}
