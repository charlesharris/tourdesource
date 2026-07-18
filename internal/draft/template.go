package draft

// The onboarding skeleton (design §7). Templates are first-class: `review` and
// `demo` modes are future templates, so the structure is data rather than a
// hard-coded sequence of writes.
//
// The skeleton exists because the product's value is *sequencing and judgment*,
// not "here are your files". Fixing the chapter structure up front means draft
// quality doesn't depend on an LLM reinventing a shape each run — and, in this
// deterministic pass, means a useful tour falls out of the map alone.

// SectionKind identifies which part of the context a chapter draws from, so the
// renderer can fill each chapter from the right evidence.
type SectionKind string

const (
	// SectionOverview — "the 30-second version": what this is, grounded in the
	// README and the framework entrypoints.
	SectionOverview SectionKind = "overview"
	// SectionSlice — "follow one operation end to end", the money chapter.
	SectionSlice SectionKind = "slice"
	// SectionLandmarks — the 4–6 key modules a newcomer will hear named.
	SectionLandmarks SectionKind = "landmarks"
	// SectionConventions — where things live, how to navigate.
	SectionConventions SectionKind = "conventions"
	// SectionSideQuests — role-specific detours, including "I'm here to fix a bug".
	SectionSideQuests SectionKind = "sidequests"
)

// ChapterSpec is one chapter of a template: its heading, the guidance a curator
// (or a later AI pass) should write to, and which evidence fills it.
type ChapterSpec struct {
	Title string
	Kind  SectionKind
	// Guidance states what this chapter is *for*. It is emitted into the draft
	// as an HTML comment so it survives to the curator and is stripped from the
	// rendered tour.
	Guidance string
}

// Template is a named chapter sequence.
type Template struct {
	Name     string
	Chapters []ChapterSpec
}

// Onboarding is the default template: the opinionated skeleton from design §7.
func Onboarding() Template {
	return Template{
		Name: "onboarding",
		Chapters: []ChapterSpec{
			{
				Title: "The 30-second version",
				Kind:  SectionOverview,
				Guidance: "What is this project, what does it do, and what shape is it? " +
					"Ground this in the README and the entrypoints below. A reader who " +
					"stops here should still be able to describe the system in a sentence.",
			},
			{
				Title: "Follow one operation end to end",
				Kind:  SectionSlice,
				Guidance: "The money chapter: one coherent vertical slice teaches more than " +
					"any architecture overview. tds proposes a trace below from naming " +
					"convention — replace it with the operation you'd actually walk a new " +
					"hire through, and say what happens at each hop.",
			},
			{
				Title: "The major landmarks",
				Kind:  SectionLandmarks,
				Guidance: "4–6 modules or boundaries a newcomer will hear named in " +
					"conversation. For each: why it exists and the one thing to know. " +
					"Prune anything that is merely large rather than important.",
			},
			{
				Title: "Where things live",
				Kind:  SectionConventions,
				Guidance: "Navigation and conventions: naming patterns, where tests are, " +
					"how to run it. Short and factual — this is the chapter people come " +
					"back to.",
			},
			{
				Title: "Side quests",
				Kind:  SectionSideQuests,
				Guidance: "Role-specific detours a reader can take or skip. The hotspots " +
					"below are where the work actually happens, which is usually where a " +
					"first bug fix lands.",
			},
		},
	}
}
