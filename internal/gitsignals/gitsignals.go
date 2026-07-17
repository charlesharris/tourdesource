// Package gitsignals derives per-file signals from a repository's git history
// (churn, age, first/last touch, and primary authors). These signals feed
// ordering heuristics and "landmark" selection in the repo map.
package gitsignals

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ErrNotARepo is wrapped into the error returned by Compute when root is not a
// git repository (or git otherwise cannot read its history). Callers may use
// errors.Is to detect this case.
var ErrNotARepo = errors.New("not a git repository")

// Signal holds the aggregated git history for a single file.
type Signal struct {
	// Path is the repo-relative path of the file.
	Path string
	// Churn is the number of commits that touched the file.
	Churn int
	// FirstCommit is the author date of the earliest commit touching the file.
	FirstCommit time.Time
	// LastCommit is the author date of the latest commit touching the file.
	LastCommit time.Time
	// AgeDays is the number of whole days between FirstCommit and now.
	AgeDays int
	// Authors lists up to the top 3 author names by commit count, most
	// frequent first, ties broken alphabetically.
	Authors []string
}

// field separators embedded into git's --pretty format. 0x01 marks the start of
// a commit header line and 0x1f separates the author name from the date; neither
// byte can legitimately appear in an author name, so parsing stays robust.
const (
	commitMark = "\x01"
	fieldSep   = "\x1f"
)

// accum is the mutable per-path tally built up while scanning history.
type accum struct {
	churn       int
	first, last time.Time
	authors     map[string]int
}

// Compute runs a single `git log` over the full history of the repository at
// root and returns per-file Signals keyed by repo-relative path.
//
// If files is non-empty, only signals for those paths are returned (still
// derived from the complete history); paths not present in history are omitted.
// If files is empty or nil, a signal is returned for every path seen in history.
//
// If root is not a git repository, the returned error wraps ErrNotARepo.
func Compute(root string, files []string) (map[string]Signal, error) {
	// One pass over history: a commit header line begins with commitMark and
	// carries "author<fieldSep>ISO8601"; the following non-empty lines are the
	// paths that commit touched, terminated by a blank line.
	cmd := exec.Command("git", "-C", root, "log", "--no-merges",
		"--name-only", "--pretty=format:"+commitMark+"%an"+fieldSep+"%aI")
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("%w: git log in %s: %s", ErrNotARepo, root,
				strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, fmt.Errorf("running git log in %s: %w", root, err)
	}

	tally := map[string]*accum{}
	var curAuthor string
	var curDate time.Time

	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, commitMark) {
			header := strings.TrimPrefix(line, commitMark)
			author, dateStr, ok := strings.Cut(header, fieldSep)
			if !ok {
				return nil, fmt.Errorf("malformed git log header: %q", line)
			}
			date, err := time.Parse(time.RFC3339, dateStr)
			if err != nil {
				return nil, fmt.Errorf("parsing commit date %q: %w", dateStr, err)
			}
			curAuthor, curDate = author, date
			continue
		}
		if line == "" {
			continue
		}
		// A path line for the current commit.
		a := tally[line]
		if a == nil {
			a = &accum{first: curDate, last: curDate, authors: map[string]int{}}
			tally[line] = a
		}
		a.churn++
		a.authors[curAuthor]++
		if curDate.Before(a.first) {
			a.first = curDate
		}
		if curDate.After(a.last) {
			a.last = curDate
		}
	}

	now := time.Now()
	result := map[string]Signal{}
	emit := func(path string, a *accum) {
		result[path] = Signal{
			Path:        path,
			Churn:       a.churn,
			FirstCommit: a.first,
			LastCommit:  a.last,
			AgeDays:     int(now.Sub(a.first).Hours() / 24),
			Authors:     topAuthors(a.authors),
		}
	}

	if len(files) > 0 {
		for _, f := range files {
			if a := tally[f]; a != nil {
				emit(f, a)
			}
		}
	} else {
		for path, a := range tally {
			emit(path, a)
		}
	}

	return result, nil
}

// topAuthors returns up to the three most frequent author names, most frequent
// first, with ties broken alphabetically for deterministic output.
func topAuthors(counts map[string]int) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > 3 {
		names = names[:3]
	}
	return names
}
