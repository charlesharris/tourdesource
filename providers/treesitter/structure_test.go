package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture writes src to a file in dir and returns dir.
func writeFixture(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

// symbolLine renders a symbol as "kind symbol L<start>-<end>" for compact,
// order-sensitive comparison.
func symbolLine(s symbol) string {
	return s.Kind + " " + s.Symbol
}

func symbolLines(syms []symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = symbolLine(s)
	}
	return out
}

func importLines(imps []importEdge) []string {
	out := make([]string, len(imps))
	for i, im := range imps {
		out[i] = im.Kind + " " + im.Target
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestStructurePerLanguage pins the qualified-name normalization for every
// grammar the fallback ships: the exact `symbol` strings are what tour anchors
// resolve against, so a change here is a breaking change for existing tours.
func TestStructurePerLanguage(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		src     string
		symbols []string
		imports []string
	}{
		{
			name: "go qualifies methods by receiver type",
			file: "inv.go",
			src: "package billing\n\nimport \"fmt\"\n\ntype Invoice struct{ ID string }\n\n" +
				"func (i *Invoice) Finalize() error { return nil }\n\n" +
				"func New(id string) *Invoice { return &Invoice{ID: id} }\n",
			symbols: []string{"type Invoice", "method Invoice.Finalize", "function New"},
			imports: []string{"import fmt"},
		},
		{
			name: "python nests methods under their class",
			file: "inv.py",
			src:  "import os\nfrom decimal import Decimal\n\nclass Invoice:\n    def finalize(self):\n        return None\n\ndef helper():\n    pass\n",
			// A def inside a class is a method; a module-level def stays a function.
			symbols: []string{"class Invoice", "method Invoice.finalize", "function helper"},
			imports: []string{"import os", "import decimal"},
		},
		{
			name: "ruby separates instance and singleton methods",
			file: "inv.rb",
			src:  "module Billing\n  class Invoice\n    def finalize\n      nil\n    end\n\n    def self.overdue\n      []\n    end\n  end\nend\n",
			// '#' for instance, '.' for singleton — matching the native Ruby provider.
			symbols: []string{
				"module Billing",
				"class Billing::Invoice",
				"method Billing::Invoice#finalize",
				"method Billing::Invoice.overdue",
			},
		},
		{
			name:    "java qualifies members with dots",
			file:    "Inv.java",
			src:     "import java.util.List;\n\npublic class Invoice {\n    public Invoice() {}\n    public void settle() {}\n}\n",
			symbols: []string{"class Invoice", "method Invoice.Invoice", "method Invoice.settle"},
			imports: []string{"import java.util.List"},
		},
		{
			name: "rust attributes impl functions to their type",
			file: "inv.rs",
			src: "use std::fmt;\n\npub mod billing {\n    pub struct Invoice { pub id: String }\n\n" +
				"    impl Invoice {\n        pub fn finalize(&self) -> bool { true }\n    }\n}\n",
			// `impl` is a scope, not a symbol: it names its members without appearing itself.
			symbols: []string{"module billing", "struct billing::Invoice", "method billing::Invoice::finalize"},
			imports: []string{"use std::fmt"},
		},
		{
			name: "c unwraps pointer declarators to the function name",
			file: "inv.c",
			src:  "#include <stdio.h>\n\nstruct Invoice { int id; };\n\nchar *finalize(int id) { return 0; }\n",
			// The name sits under pointer_declarator -> function_declarator.
			symbols: []string{"struct Invoice", "function finalize"},
			imports: []string{"include stdio.h"},
		},
		{
			name: "cpp keeps out-of-line definitions qualified once",
			file: "inv.cpp",
			src: "#include <string>\n\nnamespace billing {\nclass Invoice {\npublic:\n  void finalize();\n};\n}\n\n" +
				"void billing::Invoice::finalize() {}\n",
			// The declarator already reads "billing::Invoice::finalize"; it must not
			// be prefixed a second time.
			symbols: []string{"module billing", "class billing::Invoice", "function billing::Invoice::finalize"},
			imports: []string{"include string"},
		},
		{
			name:    "javascript nests class methods",
			file:    "inv.js",
			src:     "import { api } from './api';\n\nexport class Invoice {\n  finalize() { return true; }\n}\n\nexport function helper() {}\n",
			symbols: []string{"class Invoice", "method Invoice.finalize", "function helper"},
			imports: []string{"import ./api"},
		},
		{
			name: "typescript captures type-level declarations",
			file: "inv.ts",
			src: "import { Api } from './api';\n\ninterface Payable { amount: number; }\ntype Id = string;\n\n" +
				"class Invoice implements Payable {\n  amount = 0;\n  finalize(): void {}\n}\n",
			symbols: []string{"interface Payable", "type Id", "class Invoice", "method Invoice.finalize"},
			imports: []string{"import ./api"},
		},
		{
			name: "tsx parses jsx with the tsx grammar",
			file: "App.tsx",
			src:  "import React from 'react';\n\nexport function App() {\n  return <div className=\"x\">hi</div>;\n}\n",
			// The TypeScript grammar proper cannot parse JSX; .tsx must select TSX.
			symbols: []string{"function App"},
			imports: []string{"import react"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, tc.file, tc.src)

			ex := newExtractor()
			defer ex.close()
			got := ex.structure(structureParams{Root: dir, Files: []string{tc.file}})

			if len(got.FileErrors) != 0 {
				t.Fatalf("unexpected file errors: %+v", got.FileErrors)
			}
			if lines := symbolLines(got.Symbols); !equalStrings(lines, tc.symbols) {
				t.Errorf("symbols:\n got %v\nwant %v", lines, tc.symbols)
			}
			if tc.imports != nil {
				if lines := importLines(got.Imports); !equalStrings(lines, tc.imports) {
					t.Errorf("imports:\n got %v\nwant %v", lines, tc.imports)
				}
			}
			for _, s := range got.Symbols {
				if s.Path != tc.file {
					t.Errorf("symbol %q: path = %q, want %q", s.Symbol, s.Path, tc.file)
				}
				if s.StartLine < 1 || s.EndLine < s.StartLine {
					t.Errorf("symbol %q: bad line range %d-%d", s.Symbol, s.StartLine, s.EndLine)
				}
				if !strings.HasPrefix(s.BodyHash, "sha256:") {
					t.Errorf("symbol %q: body_hash = %q, want sha256: prefix", s.Symbol, s.BodyHash)
				}
			}
		})
	}
}

// TestStructureNeverFailsBatch is the acceptance criterion for TDS-11: no input
// makes the provider fail the request. Bad files are reported per-file and the
// good ones in the same batch still come back.
func TestStructureNeverFailsBatch(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "good.go", "package p\n\nfunc Good() {}\n")
	writeFixture(t, dir, "notes.txt", "not code at all\n")
	writeFixture(t, dir, "image.png", "\x89PNG\r\n\x1a\n\x00\x00binary junk")
	writeFixture(t, dir, "broken.rb", "class Broken\n  def x\n")
	writeFixture(t, dir, "empty.go", "")

	ex := newExtractor()
	defer ex.close()
	got := ex.structure(structureParams{
		Root:  dir,
		Files: []string{"good.go", "notes.txt", "image.png", "broken.rb", "empty.go", "gone.go", "sub/dir/missing.py"},
	})

	// The valid file's symbols survive alongside the failures.
	if lines := symbolLines(got.Symbols); !equalStrings(lines, []string{"function Good"}) {
		t.Errorf("symbols = %v, want [function Good]", lines)
	}

	errs := map[string]string{}
	for _, fe := range got.FileErrors {
		errs[fe.Path] = fe.Message
	}
	// Unreadable files are reported...
	for _, want := range []string{"gone.go", "sub/dir/missing.py"} {
		if _, ok := errs[want]; !ok {
			t.Errorf("expected a file error for %s, got %+v", want, got.FileErrors)
		}
	}
	// ...and a syntax error is reported without discarding the file.
	if _, ok := errs["broken.rb"]; !ok {
		t.Errorf("expected a syntax file error for broken.rb, got %+v", got.FileErrors)
	}
	// Files with no grammar are silently skipped: not an error, just no data.
	for _, quiet := range []string{"notes.txt", "image.png"} {
		if msg, ok := errs[quiet]; ok {
			t.Errorf("%s should be skipped silently, got error %q", quiet, msg)
		}
	}
	// An empty file is valid input with nothing in it.
	if msg, ok := errs["empty.go"]; ok {
		t.Errorf("empty.go should parse cleanly, got error %q", msg)
	}
}

// TestStructureSkipsOversizedFiles keeps a generated or vendored blob from
// pushing a resident provider into an out-of-memory kill.
func TestStructureSkipsOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "huge.go", "package p\n"+strings.Repeat("// filler\n", (maxFileBytes/10)+1))

	ex := newExtractor()
	defer ex.close()
	got := ex.structure(structureParams{Root: dir, Files: []string{"huge.go"}})

	if len(got.FileErrors) != 1 || !strings.Contains(got.FileErrors[0].Message, "exceeds") {
		t.Fatalf("want one size-limit error, got %+v", got.FileErrors)
	}
	if len(got.Symbols) != 0 {
		t.Errorf("want no symbols from a skipped file, got %d", len(got.Symbols))
	}
}

// TestBodyHashNormalization documents the drift contract: cosmetic whitespace
// edits leave the hash alone, real edits change it. The normalization matches
// the Ruby provider's so both agree on an unchanged symbol.
func TestBodyHashNormalization(t *testing.T) {
	base := "def finalize\n  nil\nend"
	same := []string{
		"def finalize\n  nil   \nend",      // trailing whitespace
		"\n\ndef finalize\n  nil\nend\n\n", // surrounding blank lines
		"def finalize\r\n  nil\r\nend",     // CRLF line endings
	}
	for _, variant := range same {
		if got, want := bodyHash(variant), bodyHash(base); got != want {
			t.Errorf("bodyHash(%q) = %s, want it to match the unchanged body", variant, got)
		}
	}
	if bodyHash("def finalize\n  0\nend") == bodyHash(base) {
		t.Error("a changed body must change the hash")
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := map[string]string{
		"a/b/inv.go":  "go",
		"inv.py":      "python",
		"app/inv.rb":  "ruby",
		"Gemfile":     "ruby",
		"config.ru":   "ruby",
		"Inv.java":    "java",
		"inv.rs":      "rust",
		"inv.c":       "c",
		"inv.h":       "c",
		"inv.cpp":     "cpp",
		"inv.hpp":     "cpp",
		"inv.js":      "javascript",
		"inv.ts":      "typescript",
		"App.tsx":     "typescript",
		"INV.GO":      "go", // extension matching is case-insensitive
		"README.md":   "",
		"notes.txt":   "",
		"Makefile":    "",
		"noextension": "",
	}
	for path, want := range cases {
		if got := detectLanguage(path); got != want {
			t.Errorf("detectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestGrammarForSelectsTSXVariant guards the one place a tds language maps to
// more than one grammar.
func TestGrammarForSelectsTSXVariant(t *testing.T) {
	sp := specFor("typescript")
	if sp == nil {
		t.Fatal("no typescript spec")
	}
	tsKey, _ := sp.grammarFor("a/inv.ts")
	tsxKey, _ := sp.grammarFor("a/App.tsx")
	if tsKey == tsxKey {
		t.Errorf("tsx must use a distinct grammar; both resolved to %q", tsKey)
	}
}

// TestSupportedLanguagesAreAllUsable catches a grammar whose ABI has drifted
// past the bindings — it would otherwise show up as silently empty structure.
func TestSupportedLanguagesAreAllUsable(t *testing.T) {
	ex := newExtractor()
	defer ex.close()
	for _, lang := range supportedLanguages() {
		sp := specFor(lang)
		if sp == nil {
			t.Errorf("advertised language %q has no spec", lang)
			continue
		}
		if p := ex.parserFor(sp.name, sp.language); p == nil {
			t.Errorf("grammar for advertised language %q is not loadable", lang)
		}
		for ext, variant := range sp.variants {
			if p := ex.parserFor(sp.name+ext, variant); p == nil {
				t.Errorf("grammar for %q variant %q is not loadable", lang, ext)
			}
		}
	}
}
