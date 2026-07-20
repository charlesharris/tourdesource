// Package narration carries assistant-written prose for things that are not
// tour stops — subsystem names and per-file summaries — between `tds draft`,
// which spends the tokens, and `tds build`, which renders the result.
//
// Why a sidecar file rather than the store: this is curated text, not measured
// fact. map.sqlite is rebuilt from the repository by `tds map` and everything in
// it is derived; narration is the opposite, and is the part of the pipeline a
// human is most likely to want to correct. Keeping it in readable JSON means a
// bad description is fixed with a text editor rather than another assistant run.
//
// It sits in the map directory with the rest of the generated output and is
// ignored by default. Note that it is the one artifact there that cannot be
// regenerated for free, so discarding that directory costs whatever the last
// --full-narration run cost.
//
// It also keeps `tds build` deterministic and offline. The assistant is only
// ever reached from `tds draft`; build reads what draft left behind.
package narration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the sidecar's name inside the map directory.
const FileName = "narration.json"

// Version is the document's schema version. A document written by a newer tds
// is not silently reinterpreted by an older one.
const Version = 1

// Doc is the whole sidecar.
type Doc struct {
	Version int `json:"version"`
	// Commit records which revision the prose was written against. It is
	// advisory: prose about a file that has since changed is stale but not
	// wrong enough to discard, and per-file hashes catch the cases that matter.
	Commit string `json:"commit,omitempty"`
	// Subsystems is keyed by Subsystem.ID, which is a slug of the derived group
	// name and so is stable as long as the grouping is.
	Subsystems map[string]Subsystem `json:"subsystems,omitempty"`
	// Files is keyed by repo-relative slash path.
	Files map[string]FileNote `json:"files,omitempty"`
}

// Subsystem is what the assistant made of one derived group. Name is optional:
// an empty Name keeps the mechanical one ("app/models"), which is the right
// outcome when the assistant had nothing better to offer.
type Subsystem struct {
	Name string `json:"name,omitempty"`
	Desc string `json:"desc,omitempty"`
}

// FileNote is a one-paragraph summary of a file, plus the hash of the content
// it was written about. The hash is what makes re-runs cheap: a file whose
// content has not changed does not need to be described again.
type FileNote struct {
	Hash    string `json:"hash"`
	Summary string `json:"summary"`
}

// Path returns the sidecar's location within a map directory.
func Path(mapDir string) string { return filepath.Join(mapDir, FileName) }

// Load reads the sidecar. A missing file is not an error — it is the ordinary
// state of a repository that has never been narrated — and yields an empty
// document ready to be written into.
func Load(path string) (*Doc, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	var d Doc
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if d.Version > Version {
		return nil, fmt.Errorf("%s was written by a newer tds (version %d, this build understands %d)",
			path, d.Version, Version)
	}
	if d.Subsystems == nil {
		d.Subsystems = map[string]Subsystem{}
	}
	if d.Files == nil {
		d.Files = map[string]FileNote{}
	}
	return &d, nil
}

// New returns an empty document.
func New() *Doc {
	return &Doc{
		Version:    Version,
		Subsystems: map[string]Subsystem{},
		Files:      map[string]FileNote{},
	}
}

// Save writes the document atomically. Narration is written after every batch
// so that an interrupted run keeps what it already paid for; a torn file at
// that cadence would be worse than no file at all.
func (d *Doc) Save(path string) error {
	d.Version = Version
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".narration-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Summary returns the stored summary for a path, or "" if there is none. This
// is what rendering uses.
//
// It deliberately does not check the hash. The hash answers "should tds spend
// tokens describing this file again?", which is a question for the narration
// pass; withholding prose at render time because the file moved on by a line
// would mostly mean showing nothing, which serves no reader. Prose goes stale
// the way a comment does, and is refreshed the same way — by re-running.
func (d *Doc) Summary(path string) string {
	return d.Files[path].Summary
}

// Fresh reports whether path already has a summary written against exactly this
// content. This is the cache check that makes a second run nearly free.
func (d *Doc) Fresh(path, hash string) bool {
	n, ok := d.Files[path]
	return ok && n.Hash == hash && n.Summary != ""
}

// Hash fingerprints file content for the freshness check.
func Hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:16])
}
