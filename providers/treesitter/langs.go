package main

import (
	"path/filepath"
	"sort"
	"strings"
	"unsafe"

	tsc "github.com/tree-sitter/tree-sitter-c/bindings/go"
	tscpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	tsrust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tsts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// defRule describes how one tree-sitter node kind becomes a tds symbol.
type defRule struct {
	// kind is the tds symbol kind reported to the core (class | module |
	// method | function | type | ...).
	kind string
	// nameField is the node field holding the symbol's identifier.
	nameField string
	// sep joins the enclosing scope to this symbol's name — "::" for Ruby/Rust
	// namespacing, "#" for Ruby instance methods, "." for Python/Java/JS.
	sep string
	// container makes this symbol's qualified name the scope for its children.
	container bool
	// unwrapDeclarator descends C/C++ declarator chains (pointer, array,
	// function) to reach the identifier underneath.
	unwrapDeclarator bool
}

// scopeRule marks a node that opens a naming scope without being a symbol
// itself — Rust's `impl Foo` contributes "Foo" to its children's paths but is
// not a symbol a reader would point at.
type scopeRule struct {
	nameField string
	sep       string
	// kind is the tds kind the scope behaves as, so functions declared inside it
	// are promoted to methods.
	kind string
}

// importRule describes how one node kind becomes an import edge.
type importRule struct {
	// field holds the import target; empty means "use the node's own text".
	field string
	kind  string
}

// langSpec is everything needed to extract structure for one language.
type langSpec struct {
	// name is the tds language name, matching internal/scan.DetectLanguage.
	name     string
	language func() unsafe.Pointer
	defs     map[string]defRule
	scopes   map[string]scopeRule
	imports  map[string]importRule
	// receiverField, when set, names the field holding a method's receiver, whose
	// type becomes the method's qualifier (Go's `func (i *Invoice) Finalize()`).
	receiverField string
	receiverKinds map[string]bool
	// variants overrides the grammar for particular extensions. One tds language
	// can span more than one grammar: .tsx is a different tree-sitter language
	// than TypeScript proper, and parsing it with the latter mangles every JSX
	// element.
	variants map[string]func() unsafe.Pointer
}

// grammarFor picks the grammar for a specific file, honouring extension
// variants. The returned key identifies the parser to cache it under.
func (s *langSpec) grammarFor(path string) (key string, language func() unsafe.Pointer) {
	ext := strings.ToLower(filepath.Ext(path))
	if v, ok := s.variants[ext]; ok {
		return s.name + ext, v
	}
	return s.name, s.language
}

// specs is the grammar registry. A language absent here is simply not claimed in
// the capabilities handshake, so the core never routes its files to us.
//
// The set is deliberately "everything we have a grammar for", including ruby,
// javascript and typescript, which have native providers: Host.ForLanguage scans
// providers in discovery order and this provider is registered last, so a native
// provider always wins and we only see those files when it is missing or failed
// to launch. That is precisely the fallback role.
var specs = []*langSpec{
	{
		name:     "go",
		language: tsgo.Language,
		defs: map[string]defRule{
			"function_declaration": {kind: "function", nameField: "name", sep: "."},
			"method_declaration":   {kind: "method", nameField: "name", sep: "."},
			"type_spec":            {kind: "type", nameField: "name", sep: "."},
		},
		imports: map[string]importRule{
			"import_spec": {field: "path", kind: "import"},
		},
		receiverField: "receiver",
		receiverKinds: map[string]bool{"method_declaration": true},
	},
	{
		name:     "python",
		language: tspython.Language,
		defs: map[string]defRule{
			"class_definition":    {kind: "class", nameField: "name", sep: ".", container: true},
			"function_definition": {kind: "function", nameField: "name", sep: "."},
		},
		imports: map[string]importRule{
			"import_statement":      {kind: "import"},
			"import_from_statement": {kind: "import"},
		},
	},
	{
		name:     "ruby",
		language: tsruby.Language,
		defs: map[string]defRule{
			"class":            {kind: "class", nameField: "name", sep: "::", container: true},
			"module":           {kind: "module", nameField: "name", sep: "::", container: true},
			"method":           {kind: "method", nameField: "name", sep: "#"},
			"singleton_method": {kind: "method", nameField: "name", sep: "."},
		},
	},
	{
		name:     "java",
		language: tsjava.Language,
		defs: map[string]defRule{
			"class_declaration":       {kind: "class", nameField: "name", sep: ".", container: true},
			"interface_declaration":   {kind: "interface", nameField: "name", sep: ".", container: true},
			"enum_declaration":        {kind: "enum", nameField: "name", sep: ".", container: true},
			"record_declaration":      {kind: "class", nameField: "name", sep: ".", container: true},
			"method_declaration":      {kind: "method", nameField: "name", sep: "."},
			"constructor_declaration": {kind: "method", nameField: "name", sep: "."},
		},
		imports: map[string]importRule{
			"import_declaration": {kind: "import"},
		},
	},
	{
		name:     "rust",
		language: tsrust.Language,
		defs: map[string]defRule{
			"mod_item":      {kind: "module", nameField: "name", sep: "::", container: true},
			"struct_item":   {kind: "struct", nameField: "name", sep: "::"},
			"enum_item":     {kind: "enum", nameField: "name", sep: "::"},
			"union_item":    {kind: "struct", nameField: "name", sep: "::"},
			"trait_item":    {kind: "trait", nameField: "name", sep: "::", container: true},
			"function_item": {kind: "function", nameField: "name", sep: "::"},
		},
		scopes: map[string]scopeRule{
			"impl_item": {nameField: "type", sep: "::", kind: "struct"},
		},
		imports: map[string]importRule{
			"use_declaration": {kind: "use"},
		},
	},
	{
		name:     "c",
		language: tsc.Language,
		defs: map[string]defRule{
			"function_definition": {kind: "function", nameField: "declarator", sep: ".", unwrapDeclarator: true},
			"struct_specifier":    {kind: "struct", nameField: "name", sep: "."},
			"enum_specifier":      {kind: "enum", nameField: "name", sep: "."},
		},
		imports: map[string]importRule{
			"preproc_include": {field: "path", kind: "include"},
		},
	},
	{
		name:     "cpp",
		language: tscpp.Language,
		defs: map[string]defRule{
			"namespace_definition": {kind: "module", nameField: "name", sep: "::", container: true},
			"class_specifier":      {kind: "class", nameField: "name", sep: "::", container: true},
			"struct_specifier":     {kind: "struct", nameField: "name", sep: "::", container: true},
			"function_definition":  {kind: "function", nameField: "declarator", sep: "::", unwrapDeclarator: true},
		},
		imports: map[string]importRule{
			"preproc_include": {field: "path", kind: "include"},
		},
	},
	{
		name:     "javascript",
		language: tsjs.Language,
		defs:     jsDefs(),
		imports: map[string]importRule{
			"import_statement": {field: "source", kind: "import"},
		},
	},
	{
		name:     "typescript",
		language: tsts.LanguageTypescript,
		defs:     tsDefs(),
		imports: map[string]importRule{
			"import_statement": {field: "source", kind: "import"},
		},
		variants: map[string]func() unsafe.Pointer{
			".tsx": tsts.LanguageTSX,
		},
	},
}

func jsDefs() map[string]defRule {
	return map[string]defRule{
		"class_declaration":              {kind: "class", nameField: "name", sep: ".", container: true},
		"function_declaration":           {kind: "function", nameField: "name", sep: "."},
		"generator_function_declaration": {kind: "function", nameField: "name", sep: "."},
		"method_definition":              {kind: "method", nameField: "name", sep: "."},
	}
}

// tsDefs is the JavaScript set plus TypeScript's type-level declarations.
func tsDefs() map[string]defRule {
	d := jsDefs()
	d["interface_declaration"] = defRule{kind: "interface", nameField: "name", sep: ".", container: true}
	d["abstract_class_declaration"] = defRule{kind: "class", nameField: "name", sep: ".", container: true}
	d["type_alias_declaration"] = defRule{kind: "type", nameField: "name", sep: "."}
	d["enum_declaration"] = defRule{kind: "enum", nameField: "name", sep: ".", container: true}
	return d
}

// specFor returns the spec for a tds language name, or nil if unsupported.
func specFor(lang string) *langSpec {
	for _, s := range specs {
		if s.name == lang {
			return s
		}
	}
	return nil
}

// supportedLanguages lists the languages advertised in the capabilities
// handshake, sorted for a stable handshake payload.
func supportedLanguages() []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.name)
	}
	sort.Strings(out)
	return out
}
