package tour

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFileExample(t *testing.T) {
	tr, err := ParseFile(filepath.Join("testdata", "example.tour.md"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	// Frontmatter: recognized keys typed, unknown keys stringified into Meta.
	if got, want := tr.Title, "A tour of Acme's billing service"; got != want {
		t.Errorf("Title = %q, want %q", got, want)
	}
	if got, want := tr.Template, "onboarding"; got != want {
		t.Errorf("Template = %q, want %q", got, want)
	}
	if got, want := tr.Audience, "new backend engineers"; got != want {
		t.Errorf("Audience = %q, want %q", got, want)
	}
	if got, want := tr.Commit, "auto"; got != want {
		t.Errorf("Commit = %q, want %q", got, want)
	}
	if got, want := tr.Meta["maintainer"], "billing-team"; got != want {
		t.Errorf("Meta[maintainer] = %q, want %q", got, want)
	}

	// Chapters.
	if got, want := len(tr.Chapters), 2; got != want {
		t.Fatalf("len(Chapters) = %d, want %d", got, want)
	}
	if got, want := tr.Chapters[0].Title, "The 30-second version"; got != want {
		t.Errorf("Chapters[0].Title = %q, want %q", got, want)
	}
	if !strings.HasPrefix(tr.Chapters[0].Intro, "Acme Billing turns usage events") {
		t.Errorf("Chapters[0].Intro = %q, want it to start with the chapter prose", tr.Chapters[0].Intro)
	}

	// First stop: anchor + focus.
	c0s0 := tr.Chapters[0].Stops[0]
	if got, want := c0s0.Anchor, "app/models/invoice.rb::Invoice"; got != want {
		t.Errorf("stop anchor = %q, want %q", got, want)
	}
	if got, want := c0s0.Focus, "def finalize"; got != want {
		t.Errorf("stop focus = %q, want %q", got, want)
	}
	if !strings.Contains(c0s0.Prose, "aggregate root") {
		t.Errorf("stop prose = %q, want it to contain the body", c0s0.Prose)
	}

	// Second chapter: two stops; the second carries the detour.
	c1 := tr.Chapters[1]
	if got, want := len(c1.Stops), 2; got != want {
		t.Fatalf("len(Chapters[1].Stops) = %d, want %d", got, want)
	}
	jobStop := c1.Stops[1]
	if got, want := jobStop.Anchor, "app/jobs/finalize_invoice_job.rb::FinalizeInvoiceJob#perform"; got != want {
		t.Errorf("job stop anchor = %q, want %q", got, want)
	}
	if got, want := len(jobStop.Detours), 1; got != want {
		t.Fatalf("len(Detours) = %d, want %d", got, want)
	}

	// Detour title + nested stop anchor.
	detour := jobStop.Detours[0]
	if got, want := detour.Title, "If you're debugging a stuck invoice"; got != want {
		t.Errorf("detour title = %q, want %q", got, want)
	}
	if got, want := len(detour.Stops), 1; got != want {
		t.Fatalf("len(detour.Stops) = %d, want %d", got, want)
	}
	if got, want := detour.Stops[0].Anchor, "app/models/invoice.rb::Invoice#with_lock"; got != want {
		t.Errorf("nested stop anchor = %q, want %q", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantSub string
	}{
		{
			name:    "unclosed stop",
			src:     "# Chapter: X\n::stop{anchor=\"a.rb::A\"}\nprose\n",
			wantSub: "unclosed block",
		},
		{
			name:    "unknown directive",
			src:     "# Chapter: X\n::frobnicate{}\n::\n",
			wantSub: "unknown directive",
		},
		{
			name:    "stop missing anchor",
			src:     "# Chapter: X\n::stop{focus=\"def x\"}\n::\n",
			wantSub: "missing required attribute \"anchor\"",
		},
		{
			name:    "stop before any chapter",
			src:     "::stop{anchor=\"a.rb::A\"}\n::\n",
			wantSub: "before any `# Chapter:`",
		},
		{
			name:    "malformed frontmatter",
			src:     "---\ntitle: [unterminated\n---\n\n# Chapter: X\n",
			wantSub: "frontmatter",
		},
		{
			name:    "unterminated frontmatter",
			src:     "---\ntitle: \"x\"\n\n# Chapter: X\n",
			wantSub: "unterminated frontmatter",
		},
		{
			name:    "stray close",
			src:     "# Chapter: X\n::\n",
			wantSub: "no open block to close",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil {
				t.Fatalf("Parse(%q) = nil error, want error containing %q", tc.src, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestParseAttrs(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    map[string]string
		wantErr bool
	}{
		{
			name: "empty",
			in:   "",
			want: map[string]string{},
		},
		{
			name: "single pair",
			in:   `anchor="a.rb::A"`,
			want: map[string]string{"anchor": "a.rb::A"},
		},
		{
			name: "multiple pairs, value with spaces",
			in:   `anchor="app/models/invoice.rb::Invoice" focus="def finalize"`,
			want: map[string]string{"anchor": "app/models/invoice.rb::Invoice", "focus": "def finalize"},
		},
		{
			name: "value with apostrophe and spaces",
			in:   `title="If you're debugging a stuck invoice"`,
			want: map[string]string{"title": "If you're debugging a stuck invoice"},
		},
		{
			name: "extra surrounding whitespace",
			in:   `   anchor="x"    focus="y"   `,
			want: map[string]string{"anchor": "x", "focus": "y"},
		},
		{
			name: "spaces around equals",
			in:   `anchor = "x"`,
			want: map[string]string{"anchor": "x"},
		},
		{
			name:    "unquoted value",
			in:      `anchor=x`,
			wantErr: true,
		},
		{
			name:    "unterminated value",
			in:      `anchor="x`,
			wantErr: true,
		},
		{
			name:    "missing equals",
			in:      `anchor "x"`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAttrs(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseAttrs(%q) = %v, nil error; want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAttrs(%q): %v", tc.in, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseAttrs(%q) = %v, want %v", tc.in, got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("parseAttrs(%q)[%q] = %q, want %q", tc.in, k, got[k], v)
				}
			}
		})
	}
}
