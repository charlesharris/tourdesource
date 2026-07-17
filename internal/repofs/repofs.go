// Package repofs reads a repository snapshot at a pinned commit and lays it out
// as lazy-loadable per-file blobs plus an index.
//
// Everything is read from the committed tree at a resolved SHA via git (using
// os/exec), never from the working tree. This makes a snapshot immutable:
// editing files after the commit does not change what the snapshot enumerates or
// what Content returns. The on-disk layout produced by WriteBlobs — a files/
// tree of blobs plus an index.json manifest — lets a viewer read the index up
// front and fetch individual file blobs on demand.
package repofs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// File is a single file in a pinned snapshot. Path is repo-relative and
// slash-separated (as git emits it); Size is the file's byte length at the
// pinned commit.
type File struct {
	Path string
	Size int64
}

// Snapshot is the set of files in a repository at a pinned commit. Root is the
// repository root passed to Read, Commit is the resolved 40-hex SHA, and Files
// is the committed file set sorted by Path.
type Snapshot struct {
	Root   string
	Commit string
	Files  []File
}

// Resolve resolves commit to a concrete SHA within the git repository at root.
//
// An empty commit or the literal "auto" means HEAD. Any other value is treated
// as a ref or SHA and resolved to the commit it names. It returns an error if
// root is not a git repository or the ref is unknown.
func Resolve(root, commit string) (string, error) {
	if commit == "" || commit == "auto" {
		return runGit(root, "rev-parse", "HEAD")
	}
	// ^{commit} dereferences tags/refs down to a commit object; if that fails
	// (e.g. an abbreviated SHA), fall back to a plain verify.
	sha, err := runGit(root, "rev-parse", commit+"^{commit}")
	if err == nil {
		return sha, nil
	}
	sha, verr := runGit(root, "rev-parse", "--verify", commit)
	if verr != nil {
		return "", fmt.Errorf("resolving commit %q in %s: %w", commit, root, err)
	}
	return sha, nil
}

// Read resolves commit and enumerates the repository's files at that commit,
// returning a Snapshot with each File's Size filled from the committed tree.
func Read(root, commit string) (*Snapshot, error) {
	sha, err := Resolve(root, commit)
	if err != nil {
		return nil, err
	}
	files, err := listTree(root, sha)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return &Snapshot{Root: root, Commit: sha, Files: files}, nil
}

// listTree enumerates the files (blobs) in the committed tree at sha, filling
// Path and Size. It uses `ls-tree -r -l -z`, whose records are
// "<mode> SP <type> SP <sha> SP <size> TAB <path>" separated by NUL bytes; the
// NUL termination keeps unusual paths intact.
func listTree(root, sha string) ([]File, error) {
	cmd := exec.Command("git", "-C", root, "ls-tree", "-r", "-l", "-z", sha)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git ls-tree at %s in %s: %w: %s", sha, root, err, strings.TrimSpace(stderr.String()))
	}

	records := bytes.Split(stdout.Bytes(), []byte{0})
	files := make([]File, 0, len(records))
	for _, rec := range records {
		if len(rec) == 0 {
			continue
		}
		// Split "<meta>\t<path>" first: the path may contain spaces, the meta
		// fields never do.
		meta, path, ok := strings.Cut(string(rec), "\t")
		if !ok {
			return nil, fmt.Errorf("malformed ls-tree record %q", rec)
		}
		fields := strings.Fields(meta)
		if len(fields) != 4 {
			return nil, fmt.Errorf("malformed ls-tree meta %q", meta)
		}
		// fields: mode, type, sha, size. Only blobs carry a numeric size; with
		// -r there should be no tree/submodule entries, but guard anyway.
		size, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			continue
		}
		files = append(files, File{Path: path, Size: size})
	}
	return files, nil
}

// Content returns the bytes of path as recorded at the pinned commit, via
// `git show <sha>:<path>`. It reads the committed blob, so it is unaffected by
// any later edits to the working tree.
func (s *Snapshot) Content(path string) ([]byte, error) {
	cmd := exec.Command("git", "-C", s.Root, "show", s.Commit+":"+path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git show %s:%s in %s: %w: %s", s.Commit, path, s.Root, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// indexEntry is one record in index.json.
type indexEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// WriteBlobs materializes the snapshot under outDir as a lazy-load layout: each
// file's committed content is written to <outDir>/files/<path> (creating parent
// directories), and <outDir>/index.json is written as a JSON array of
// {path, size} for every file. It returns the path to index.json.
func (s *Snapshot) WriteBlobs(outDir string) (indexPath string, err error) {
	entries := make([]indexEntry, 0, len(s.Files))
	for _, f := range s.Files {
		content, err := s.Content(f.Path)
		if err != nil {
			return "", err
		}
		dst := filepath.Join(outDir, "files", filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", fmt.Errorf("creating blob dir for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			return "", fmt.Errorf("writing blob %s: %w", f.Path, err)
		}
		entries = append(entries, indexEntry{Path: f.Path, Size: f.Size})
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encoding index: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("creating out dir %s: %w", outDir, err)
	}
	indexPath = filepath.Join(outDir, "index.json")
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		return "", fmt.Errorf("writing index %s: %w", indexPath, err)
	}
	return indexPath, nil
}

// runGit runs `git -C root <args...>`, returning trimmed stdout or a wrapped
// error that includes git's stderr.
func runGit(root string, args ...string) (string, error) {
	full := append([]string{"-C", root}, args...)
	cmd := exec.Command("git", full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), root, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
