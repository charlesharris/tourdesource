// Package anchor parses and resolves tour anchors against the repo map.
//
// An anchor points a tour stop at code. It is symbol-first, with a line-range
// fallback form (design §4.2):
//
//	path/to/file.rb::Invoice#finalize   symbol anchor (instance method)
//	path/to/file.rb::Invoice.overdue    symbol anchor (singleton method)
//	path/to/file.rb::Billing::Invoice   symbol anchor (namespaced class)
//	path/to/file.rb:40-52               line-range anchor
//	path/to/file.rb:40                  single-line anchor
//
// Resolution looks a symbol up in the map to get its concrete line range.
// Symbols are matched exactly first, then loosely (treating the `#`/`.` member
// separator as interchangeable) so an author who writes `.` where the provider
// emitted `#` still resolves. Unresolved symbol anchors are flagged, never
// guessed.
package anchor

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charlesharris/tourdesource/internal/protocol"
)

// Anchor is a parsed anchor reference. Exactly one of Symbol (symbol anchor) or
// the line fields (IsLineRange) is meaningful.
type Anchor struct {
	Path        string
	Symbol      string // qualified symbol path; empty for a line-range anchor
	IsLineRange bool
	Line        int // line-range start
	EndLine     int // line-range end (== Line for a single-line anchor)
}

// Parse parses an anchor string. The path/symbol delimiter is the first "::";
// a line-range anchor uses a single ":" before a "start[-end]" spec.
func Parse(s string) (Anchor, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Anchor{}, fmt.Errorf("empty anchor")
	}

	// Symbol anchor: everything before the first "::" is the path. File paths
	// don't contain "::", so this split is unambiguous even for Module::Class.
	if i := strings.Index(s, "::"); i >= 0 {
		path := strings.TrimSpace(s[:i])
		sym := strings.TrimSpace(s[i+2:])
		if path == "" || sym == "" {
			return Anchor{}, fmt.Errorf("malformed symbol anchor %q: want path::Symbol", s)
		}
		return Anchor{Path: filepath.ToSlash(path), Symbol: sym}, nil
	}

	// Line-range anchor: path:start[-end].
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return Anchor{}, fmt.Errorf("anchor %q: want %q or %q", s, "path::Symbol", "path:line[-line]")
	}
	path := strings.TrimSpace(s[:i])
	spec := strings.TrimSpace(s[i+1:])
	if path == "" || spec == "" {
		return Anchor{}, fmt.Errorf("malformed line anchor %q", s)
	}
	start, end, err := parseLineSpec(spec)
	if err != nil {
		return Anchor{}, fmt.Errorf("anchor %q: %w", s, err)
	}
	return Anchor{Path: filepath.ToSlash(path), IsLineRange: true, Line: start, EndLine: end}, nil
}

func parseLineSpec(spec string) (start, end int, err error) {
	if j := strings.Index(spec, "-"); j >= 0 {
		a, e1 := strconv.Atoi(strings.TrimSpace(spec[:j]))
		b, e2 := strconv.Atoi(strings.TrimSpace(spec[j+1:]))
		if e1 != nil || e2 != nil || a <= 0 || b < a {
			return 0, 0, fmt.Errorf("invalid line range %q", spec)
		}
		return a, b, nil
	}
	n, e := strconv.Atoi(spec)
	if e != nil || n <= 0 {
		return 0, 0, fmt.Errorf("invalid line number %q", spec)
	}
	return n, n, nil
}

// Kind classifies a resolution outcome.
type Kind string

const (
	KindSymbol     Kind = "symbol"
	KindLineRange  Kind = "line-range"
	KindUnresolved Kind = "unresolved"
)

// Resolved is the outcome of resolving an anchor against the map.
type Resolved struct {
	Path      string
	StartLine int
	EndLine   int
	Symbol    string // resolved qualified symbol (symbol anchors)
	Kind      Kind
	Loose     bool   // matched via the #/. loose fallback
	Reason    string // why it is unresolved (Kind == KindUnresolved)
}

// Resolver resolves anchors against a fixed set of map symbols.
type Resolver struct {
	exact map[string]map[string]protocol.Symbol   // path -> symbol -> symbol
	loose map[string]map[string][]protocol.Symbol // path -> looseKey -> symbols
}

// NewResolver indexes the given symbols (typically store.Symbols()) for lookup.
func NewResolver(symbols []protocol.Symbol) *Resolver {
	r := &Resolver{
		exact: map[string]map[string]protocol.Symbol{},
		loose: map[string]map[string][]protocol.Symbol{},
	}
	for _, s := range symbols {
		if r.exact[s.Path] == nil {
			r.exact[s.Path] = map[string]protocol.Symbol{}
			r.loose[s.Path] = map[string][]protocol.Symbol{}
		}
		r.exact[s.Path][s.Symbol] = s
		lk := looseKey(s.Symbol)
		r.loose[s.Path][lk] = append(r.loose[s.Path][lk], s)
	}
	return r
}

// looseKey collapses the instance/singleton member separator so "Invoice#finalize"
// and "Invoice.finalize" share a key. Method names can't contain ".", so this is
// safe.
func looseKey(symbol string) string {
	return strings.ReplaceAll(symbol, "#", ".")
}

// Resolve parses and resolves an anchor string. A parse error is returned;
// an unresolved-but-well-formed anchor yields a Resolved with KindUnresolved.
func (r *Resolver) Resolve(anchorStr string) (Resolved, error) {
	a, err := Parse(anchorStr)
	if err != nil {
		return Resolved{}, err
	}
	return r.ResolveAnchor(a), nil
}

// ResolveAnchor resolves an already-parsed anchor.
func (r *Resolver) ResolveAnchor(a Anchor) Resolved {
	if a.IsLineRange {
		return Resolved{Path: a.Path, StartLine: a.Line, EndLine: a.EndLine, Kind: KindLineRange}
	}

	if byName, ok := r.exact[a.Path]; ok {
		if s, ok := byName[a.Symbol]; ok {
			return resolvedSymbol(a.Path, s, false)
		}
	}

	if byLoose, ok := r.loose[a.Path]; ok {
		switch cands := byLoose[looseKey(a.Symbol)]; len(cands) {
		case 1:
			return resolvedSymbol(a.Path, cands[0], true)
		case 0:
			// fall through to unresolved
		default:
			return Resolved{
				Path: a.Path, Symbol: a.Symbol, Kind: KindUnresolved,
				Reason: fmt.Sprintf("ambiguous: %d symbols match %q loosely in %s", len(cands), a.Symbol, a.Path),
			}
		}
	}

	reason := fmt.Sprintf("no symbol %q in %s", a.Symbol, a.Path)
	if _, mapped := r.exact[a.Path]; !mapped {
		reason = fmt.Sprintf("no symbols mapped for %s (anchor %q)", a.Path, a.Symbol)
	}
	return Resolved{Path: a.Path, Symbol: a.Symbol, Kind: KindUnresolved, Reason: reason}
}

func resolvedSymbol(path string, s protocol.Symbol, loose bool) Resolved {
	return Resolved{
		Path:      path,
		StartLine: s.StartLine,
		EndLine:   s.EndLine,
		Symbol:    s.Symbol,
		Kind:      KindSymbol,
		Loose:     loose,
	}
}
