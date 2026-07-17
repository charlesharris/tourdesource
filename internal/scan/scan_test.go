package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"a.rb", "ruby"},
		{"lib/foo.rake", "ruby"},
		{"pkg.gemspec", "ruby"},
		{"Gemfile", "ruby"},
		{"path/to/Gemfile", "ruby"},
		{"Rakefile", "ruby"},
		{"config.ru", "ruby"},
		{"web/app.jsx", "javascript"},
		{"index.mjs", "javascript"},
		{"main.ts", "typescript"},
		{"component.tsx", "typescript"},
		{"main.go", "go"},
		{"README.md", "markdown"},
		{"data.JSON", "json"}, // case-insensitive extension
		{"weird.xyz", ""},     // unknown extension
		{"noext", ""},
	}
	for _, tt := range tests {
		if got := DetectLanguage(tt.path); got != tt.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestScanRespectsGitignore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()

	// Isolate git from any ambient user/global config.
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=tds",
		"GIT_AUTHOR_EMAIL=tds@example.com",
		"GIT_COMMITTER_NAME=tds",
		"GIT_COMMITTER_EMAIL=tds@example.com",
	)
	gitInit := exec.Command("git", "-c", "init.defaultBranch=main", "init")
	gitInit.Dir = dir
	gitInit.Env = env
	if out, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	writeFile(t, dir, "a.rb", "puts 'hi'\n")
	writeFile(t, dir, "web/app.jsx", "export default 1;\n")
	writeFile(t, dir, "README.md", "# hi\n")
	writeFile(t, dir, "debug.log", "noise\n")
	writeFile(t, dir, ".gitignore", "*.log\n")

	files, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	byPath := map[string]File{}
	for _, f := range files {
		byPath[f.Path] = f
	}

	wantLang := map[string]string{
		"a.rb":        "ruby",
		"web/app.jsx": "javascript",
		"README.md":   "markdown",
	}
	for path, lang := range wantLang {
		f, ok := byPath[path]
		if !ok {
			t.Errorf("expected %q in scan results", path)
			continue
		}
		if f.Language != lang {
			t.Errorf("%q language = %q, want %q", path, f.Language, lang)
		}
	}

	if _, ok := byPath["debug.log"]; ok {
		t.Errorf("debug.log should be excluded by .gitignore, got %+v", byPath["debug.log"])
	}
}

func TestScanFallbackNonGit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "main.go", "package main\n")
	writeFile(t, dir, "sub/util.py", "x = 1\n")

	files, err := Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	byPath := map[string]File{}
	for _, f := range files {
		byPath[f.Path] = f
	}
	if f, ok := byPath["main.go"]; !ok || f.Language != "go" {
		t.Errorf("expected main.go (go), got %+v (present=%v)", f, ok)
	}
	if f, ok := byPath["sub/util.py"]; !ok || f.Language != "python" {
		t.Errorf("expected sub/util.py (python), got %+v (present=%v)", f, ok)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
