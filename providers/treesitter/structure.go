package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	ts "github.com/tree-sitter/go-tree-sitter"
)

// maxFileBytes bounds what we hand to tree-sitter. Parsing allocates several
// times the source size; a vendored bundle or generated blob is not worth an
// out-of-memory kill of a resident provider.
const maxFileBytes = 4 << 20

// extractor owns one parser per language. Parsers are stateful C objects and are
// reused across requests to amortize setup; the protocol serializes calls, so a
// single set needs no locking.
type extractor struct {
	parsers map[string]*ts.Parser
}

func newExtractor() *extractor {
	return &extractor{parsers: map[string]*ts.Parser{}}
}

func (e *extractor) close() {
	for _, p := range e.parsers {
		p.Close()
	}
	e.parsers = nil
}

// parserFor returns the parser for a grammar, creating it on first use. It
// returns nil if the grammar rejects the binding's language version, which is
// treated as "unsupported" rather than an error, and caches that verdict so a
// bad grammar is only probed once.
func (e *extractor) parserFor(key string, language func() unsafe.Pointer) *ts.Parser {
	if p, ok := e.parsers[key]; ok {
		return p
	}
	p := ts.NewParser()
	if err := p.SetLanguage(ts.NewLanguage(language())); err != nil {
		p.Close()
		e.parsers[key] = nil
		return nil
	}
	e.parsers[key] = p
	return p
}

// structure implements the structure op over a batch of files. Every failure
// mode is per-file: an unreadable file, an unsupported language, an oversized
// file, or a syntax error yields a file_error and, where possible, whatever
// symbols were still recoverable. The batch itself never fails.
func (e *extractor) structure(params structureParams) structureResult {
	out := structureResult{
		Symbols:     []symbol{},
		Imports:     []importEdge{},
		Entrypoints: []entrypoint{},
		FileErrors:  []fileError{},
	}
	root := params.Root
	if root == "" {
		root = "."
	}

	for _, rel := range params.Files {
		sp := specFor(detectLanguage(rel))
		if sp == nil {
			// Not a language we have a grammar for. The core routes by language
			// so this is unusual, but it is a no-op, never an error.
			continue
		}
		parser := e.parserFor(sp.grammarFor(rel))
		if parser == nil {
			out.FileErrors = append(out.FileErrors, fileError{
				Path: rel, Message: "no usable " + sp.name + " grammar",
			})
			continue
		}

		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			out.FileErrors = append(out.FileErrors, fileError{
				Path: rel, Message: "cannot read " + rel,
			})
			continue
		}
		if len(src) > maxFileBytes {
			out.FileErrors = append(out.FileErrors, fileError{
				Path:    rel,
				Message: fmt.Sprintf("skipped: %d bytes exceeds %d-byte limit", len(src), maxFileBytes),
			})
			continue
		}

		tree := parser.Parse(src, nil)
		if tree == nil {
			out.FileErrors = append(out.FileErrors, fileError{Path: rel, Message: "parse failed"})
			continue
		}
		rootNode := tree.RootNode()
		if rootNode.HasError() {
			// Recoverable: tree-sitter still produces a usable tree around the
			// damage, so we report the fault and keep the symbols we can see.
			out.FileErrors = append(out.FileErrors, fileError{
				Path: rel, Message: "syntax errors; structure may be incomplete",
			})
		}
		e.walk(rootNode, sp, src, rel, "", "", &out)
		tree.Close()
	}

	return out
}

// typeLikeKinds are the container kinds whose direct function members are
// methods rather than free functions. Grammars that spell both the same way
// (Python's and JavaScript's `function_definition`) need the enclosing container
// to tell them apart.
var typeLikeKinds = map[string]bool{
	"class":     true,
	"interface": true,
	"enum":      true,
	"struct":    true,
	"trait":     true,
}

// walk descends the tree, turning matching node kinds into symbols and imports.
// scope is the qualified name of the enclosing container ("" at file level),
// which children prepend to their own names; scopeKind is that container's tds
// kind, used to promote a function to a method.
func (e *extractor) walk(n *ts.Node, sp *langSpec, src []byte, rel, scope, scopeKind string, out *structureResult) {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		kind := child.Kind()

		if rule, ok := sp.imports[kind]; ok {
			if target := importTarget(child, rule, src); target != "" {
				out.Imports = append(out.Imports, importEdge{Path: rel, Target: target, Kind: rule.kind})
			}
			continue
		}

		if rule, ok := sp.defs[kind]; ok {
			name := declName(child, rule, src)
			if name == "" {
				// A malformed or anonymous declaration: nothing nameable to
				// point at, but its children may still hold symbols.
				e.walk(child, sp, src, rel, scope, scopeKind, out)
				continue
			}

			// A Go method belongs to its receiver's type, not to lexical scope.
			qualifier := scope
			if sp.receiverKinds[kind] {
				if recv := receiverType(child, sp, src); recv != "" {
					qualifier = recv
				}
			}
			qualified := qualify(qualifier, rule.sep, name)

			symKind := rule.kind
			if symKind == "function" && typeLikeKinds[scopeKind] {
				symKind = "method"
			}

			out.Symbols = append(out.Symbols, symbol{
				Path:      rel,
				Kind:      symKind,
				Name:      name,
				Symbol:    qualified,
				StartLine: int(child.StartPosition().Row) + 1,
				EndLine:   int(child.EndPosition().Row) + 1,
				BodyHash:  bodyHash(child.Utf8Text(src)),
			})

			next, nextKind := scope, scopeKind
			if rule.container {
				next, nextKind = qualified, rule.kind
			}
			e.walk(child, sp, src, rel, next, nextKind, out)
			continue
		}

		if rule, ok := sp.scopes[kind]; ok {
			// Opens a naming scope without being a symbol itself (Rust `impl`).
			next := scope
			if f := child.ChildByFieldName(rule.nameField); f != nil {
				next = qualify(scope, rule.sep, f.Utf8Text(src))
			}
			e.walk(child, sp, src, rel, next, rule.kind, out)
			continue
		}

		e.walk(child, sp, src, rel, scope, scopeKind, out)
	}
}

// declName reads a declaration's identifier, unwrapping C/C++ declarator chains
// (`*`, `[]`, `()`) when the rule calls for it.
func declName(n *ts.Node, rule defRule, src []byte) string {
	f := n.ChildByFieldName(rule.nameField)
	if f == nil {
		return ""
	}
	if rule.unwrapDeclarator {
		if f = unwrapDeclarator(f); f == nil {
			return ""
		}
	}
	return f.Utf8Text(src)
}

// declaratorWrappers are the C/C++ nodes that sit between a function definition
// and the identifier it declares.
var declaratorWrappers = map[string]bool{
	"function_declarator":      true,
	"pointer_declarator":       true,
	"array_declarator":         true,
	"parenthesized_declarator": true,
	"reference_declarator":     true,
	"init_declarator":          true,
}

// unwrapDeclarator descends declarator wrappers to the identifier beneath, and
// returns nil if the chain bottoms out in something unnameable.
func unwrapDeclarator(n *ts.Node) *ts.Node {
	for n != nil && declaratorWrappers[n.Kind()] {
		next := n.ChildByFieldName("declarator")
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

// receiverType extracts the type name from a Go method receiver, seeing through
// pointer and generic wrappers so `(i *Invoice[T])` still yields "Invoice".
func receiverType(n *ts.Node, sp *langSpec, src []byte) string {
	recv := n.ChildByFieldName(sp.receiverField)
	if recv == nil {
		return ""
	}
	if id := firstDescendantOfKind(recv, "type_identifier"); id != nil {
		return id.Utf8Text(src)
	}
	return ""
}

// firstDescendantOfKind returns the first named node of the given kind in
// pre-order, or nil.
func firstDescendantOfKind(n *ts.Node, kind string) *ts.Node {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == kind {
			return child
		}
		if found := firstDescendantOfKind(child, kind); found != nil {
			return found
		}
	}
	return nil
}

// importTarget pulls the module path out of an import node and strips the
// quoting the surrounding syntax carries (`"fmt"`, `<stdio.h>`, `'./api'`).
func importTarget(n *ts.Node, rule importRule, src []byte) string {
	var target *ts.Node
	if rule.field != "" {
		target = n.ChildByFieldName(rule.field)
	} else if n.NamedChildCount() > 0 {
		target = n.NamedChild(0)
	}
	if target == nil {
		return ""
	}
	return strings.Trim(target.Utf8Text(src), `"'<>`)
}

// qualify joins an enclosing scope to a name. A name that already carries the
// separator is treated as pre-qualified — C++ out-of-line definitions
// (`void Invoice::finalize()`) name their own container.
func qualify(scope, sep, name string) string {
	if scope == "" {
		return name
	}
	if sep != "" && strings.HasPrefix(name, scope+sep) {
		return name
	}
	return scope + sep + name
}

// bodyHash hashes a symbol's normalized source — trailing whitespace and
// leading/trailing blank lines stripped — so cosmetic edits don't register as
// drift while real changes do (design §5.3). The normalization matches the Ruby
// provider's so the two agree on an unchanged symbol.
func bodyHash(text string) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// byExtension maps a lower-cased extension to a tds language name. It mirrors
// internal/scan.DetectLanguage for the languages this provider has grammars for:
// the core routes files to us by its own detection, and we re-derive the
// language here because the protocol's structure params carry paths, not
// languages.
var byExtension = map[string]string{
	".go":      "go",
	".py":      "python",
	".pyi":     "python",
	".rb":      "ruby",
	".rake":    "ruby",
	".gemspec": "ruby",
	".java":    "java",
	".rs":      "rust",
	".c":       "c",
	".h":       "c",
	".cc":      "cpp",
	".cpp":     "cpp",
	".cxx":     "cpp",
	".hpp":     "cpp",
	".hh":      "cpp",
	".js":      "javascript",
	".jsx":     "javascript",
	".mjs":     "javascript",
	".cjs":     "javascript",
	".ts":      "typescript",
	".tsx":     "typescript",
}

// byFilename covers extensionless files the core also classifies.
var byFilename = map[string]string{
	"Gemfile":   "ruby",
	"Rakefile":  "ruby",
	"config.ru": "ruby",
}

// detectLanguage returns the tds language for a path, or "" if this provider has
// no grammar for it.
func detectLanguage(path string) string {
	base := filepath.Base(filepath.FromSlash(path))
	if lang, ok := byFilename[base]; ok {
		return lang
	}
	return byExtension[strings.ToLower(filepath.Ext(base))]
}
