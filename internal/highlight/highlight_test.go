package highlight

import (
	"strings"
	"testing"
)

const rubySnippet = "class Foo\n  def bar\n    1\n  end\nend\n"

func TestHighlightRuby(t *testing.T) {
	res, err := Highlight(rubySnippet, "ruby")
	if err != nil {
		t.Fatalf("Highlight: %v", err)
	}
	if res.HTML == "" {
		t.Fatal("HTML is empty")
	}
	// Class-based chroma markup: keywords ("class", "def", "end") become
	// <span class="k">. Its presence proves the output is tokenized, not
	// plain escaped text.
	if !strings.Contains(res.HTML, `class="k"`) {
		t.Errorf("HTML lacks chroma keyword span class=\"k\":\n%s", res.HTML)
	}
	if res.Lines != 5 {
		t.Errorf("Lines = %d, want 5", res.Lines)
	}
}

func TestHighlightLanguages(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		language string
		want     int
	}{
		{
			name:     "javascript",
			source:   "function f() {\n  return 1;\n}\n",
			language: "javascript",
			want:     3,
		},
		{
			name:     "go",
			source:   "package main\n\nfunc main() {\n}\n",
			language: "go",
			want:     4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := Highlight(tt.source, tt.language)
			if err != nil {
				t.Fatalf("Highlight: %v", err)
			}
			if res.HTML == "" {
				t.Fatal("HTML is empty")
			}
			if res.Lines != tt.want {
				t.Errorf("Lines = %d, want %d", res.Lines, tt.want)
			}
		})
	}
}

func TestHighlightUnknownLanguageFallsBack(t *testing.T) {
	src := "one\ntwo\nthree\n"
	res, err := Highlight(src, "brainfuck-xyz")
	if err != nil {
		t.Fatalf("Highlight: unexpected error for unknown language: %v", err)
	}
	if res.HTML == "" {
		t.Fatal("HTML is empty")
	}
	if res.Lines != 3 {
		t.Errorf("Lines = %d, want 3", res.Lines)
	}
}

func TestHighlightLineAddressability(t *testing.T) {
	res, err := Highlight(rubySnippet, "ruby")
	if err != nil {
		t.Fatalf("Highlight: %v", err)
	}
	// Each source line is wrapped in its own <span class="line">, so the
	// number of per-line elements must equal Lines.
	if got := strings.Count(res.HTML, `class="line"`); got != res.Lines {
		t.Errorf("per-line element count = %d, want %d\n%s", got, res.Lines, res.HTML)
	}
	// Each line carries an anchor id so the viewer can scroll to line N by
	// its 1-based number. Confirm the anchors for the first and last lines.
	if !strings.Contains(res.HTML, `id="L1"`) {
		t.Errorf("HTML lacks line-1 anchor id=\"L1\":\n%s", res.HTML)
	}
	if !strings.Contains(res.HTML, `id="L5"`) {
		t.Errorf("HTML lacks line-5 anchor id=\"L5\":\n%s", res.HTML)
	}
}

func TestStylesheetCSS(t *testing.T) {
	css := StylesheetCSS()
	if css == "" {
		t.Fatal("StylesheetCSS is empty")
	}
	// The stylesheet must define the chroma classes the HTML references.
	if !strings.Contains(css, "@media (prefers-color-scheme: dark)") {
		t.Error("stylesheet should include a dark-theme layer")
	}
	if !strings.Contains(css, ".chroma") {
		t.Errorf("CSS lacks a chroma class selector (.chroma):\n%s", css)
	}
}
