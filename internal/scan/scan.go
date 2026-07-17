// Package scan walks a repository and produces its file list annotated with
// detected languages, respecting .gitignore.
//
// Enumeration prefers git: `git ls-files --cached --others --exclude-standard`
// lists tracked plus untracked-but-not-ignored files, so ignored paths are
// dropped for free. When the root is not a git repository, scan falls back to a
// best-effort filesystem walk (skipping .git) that does not interpret
// .gitignore. Language detection is by file extension and a handful of special
// filenames (Gemfile, Rakefile, config.ru).
package scan

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// File is a single enumerated repository file. Path is repo-relative and
// slash-separated, matching what `git ls-files` emits.
type File struct {
	Path     string
	Size     int64
	Language string
}

// Scan enumerates the files under root, respecting .gitignore, and returns them
// with their size and detected language, sorted by Path for determinism.
//
// It enumerates via git when root is a git repository, and otherwise falls back
// to a filesystem walk that skips the .git directory. Directories are not
// returned. Files that vanish or cannot be stat'd between enumeration and stat
// are skipped rather than failing the whole scan.
func Scan(root string) ([]File, error) {
	paths, err := gitFiles(root)
	if err != nil {
		// Not a git repo (or git unavailable): best-effort filesystem walk.
		paths, err = walkFiles(root)
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", root, err)
		}
	}

	files := make([]File, 0, len(paths))
	for _, rel := range paths {
		if rel == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			// Raced with a delete, or a broken symlink: skip it.
			continue
		}
		if info.IsDir() {
			continue
		}
		files = append(files, File{
			Path:     rel,
			Size:     info.Size(),
			Language: DetectLanguage(rel),
		})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// gitFiles lists tracked and untracked-but-not-ignored files under root using
// git, returning repo-relative slash-separated paths. It returns an error if
// root is not a git repository or git is otherwise unable to enumerate it, so
// callers can fall back to a filesystem walk.
func gitFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w: %s", root, err, strings.TrimSpace(stderr.String()))
	}

	raw := bytes.Split(stdout.Bytes(), []byte{0})
	paths := make([]string, 0, len(raw))
	for _, p := range raw {
		if len(p) == 0 {
			continue
		}
		paths = append(paths, string(p))
	}
	return paths, nil
}

// walkFiles is the non-git fallback: it walks root with filepath.WalkDir,
// skipping the .git directory, and returns repo-relative slash-separated file
// paths. It does not interpret .gitignore.
func walkFiles(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", path, err)
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", root, err)
	}
	return paths, nil
}

// byFilename maps special filenames (no useful extension) to a language.
var byFilename = map[string]string{
	"Gemfile":   "ruby",
	"Rakefile":  "ruby",
	"config.ru": "ruby",
}

// byExtension maps a lower-cased file extension (including the leading dot) to a
// language name.
var byExtension = map[string]string{
	".rb":      "ruby",
	".rake":    "ruby",
	".gemspec": "ruby",
	".js":      "javascript",
	".jsx":     "javascript",
	".mjs":     "javascript",
	".cjs":     "javascript",
	".ts":      "typescript",
	".tsx":     "typescript",
	".go":      "go",
	".py":      "python",
	".md":      "markdown",
	".json":    "json",
	".yml":     "yaml",
	".yaml":    "yaml",
	".html":    "html",
	".css":     "css",
}

// DetectLanguage returns the language for a path or filename, matched by special
// filename first (Gemfile, Rakefile, config.ru) and then by extension. Extension
// matching is case-insensitive. Unknown files yield the empty string.
func DetectLanguage(path string) string {
	base := filepath.Base(filepath.FromSlash(path))
	if lang, ok := byFilename[base]; ok {
		return lang
	}
	ext := strings.ToLower(filepath.Ext(base))
	return byExtension[ext]
}
