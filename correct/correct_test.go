package correct

import "testing"

// The dictionary used across tests is a plain list of real terms — no
// hand-written aliases. Acronym pronunciations (CGo → "seego") are generated
// automatically by spokenTerm/spokenSpan.
var testLines = []string{
	"OpenTelemetry",
	"OpenAI",
	"deduplication",
	"Zee",
	"Bun",
	"AppKit",
	"ChatGPT",
	"Node.js",
	"CGo",
	"ANE",
	"R&D",
}

func apply(t *testing.T, text string) string {
	t.Helper()
	return Parse(testLines).Apply(text)
}

func TestCorpusHighConfidencePairs(t *testing.T) {
	// Real mishearings from zee-wer-corpus pairs.tsv (high confidence).
	cases := []struct{ in, want string }{
		{"look at OpenTechnetic Contribute docs", "look at OpenTelemetry Contribute docs"},
		{"does this did application happens", "does this deduplication happens"},
		{"the Seego appkit was done", "the CGo AppKit was done"}, // via generated spoken key
		{"I run z from main branch", "I run Zee from main branch"}, // "z" expands to "zee"
	}
	for _, c := range cases {
		if got := apply(t, c.in); got != c.want {
			t.Errorf("Apply(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNgramMerge(t *testing.T) {
	// N-grams cap at 3 words, so "Chat G P T" (4 words) is out of reach —
	// same limitation as Handy.
	if got := apply(t, "use Chat GPT for this"); got != "use ChatGPT for this" {
		t.Errorf("got %q", got)
	}
	if got := apply(t, "Open AI GPT model"); got != "OpenAI GPT model" {
		t.Errorf("got %q", got)
	}
}

func TestCommonWordsNotRewritten(t *testing.T) {
	// False positives observed on the real corpus (zee-wer-corpus, 2026-08-15
	// eval): common-word spans and short-key fuzz must never be rewritten.
	for _, text := range []string{
		"it has been done",             // been ↛ Bun (soundex-equal)
		"we spend a lot",               // spend ↛ span-style entries
		"the sound is fine",            //
		"for M1 and a perfect one",     // and a ↛ ANE (via a&e "aande" variant)
		"test it and do we have any",   // and do ↛ ANE
		"the design note is the same",  // note is ↛ Node.js (soundex-equal)
		"it's not easy to look at it",  // not easy ↛ Node.js
		"Now does this happen",         // now does ↛ Node.js
		"Ah, okay. Maybe not present.", // ah ↛ ANE (short key = exact only)
	} {
		if got := apply(t, text); got != text {
			t.Errorf("Apply(%q) = %q, want unchanged", text, got)
		}
	}
}

func TestSingleCapitalLetterUsesDictCasing(t *testing.T) {
	if got := apply(t, "I remember in Z we have default language"); got != "I remember in Zee we have default language" {
		t.Errorf("got %q", got)
	}
}

func TestPunctuationBoundaryAndPreservation(t *testing.T) {
	if got := apply(t, "using seego, then AppKit."); got != "using CGo, then AppKit." {
		t.Errorf("got %q", got)
	}
	// The comma after "B" must stop the n-gram from consuming "che".
	d := Parse([]string{"ChargeBee"})
	if got := d.Apply("name is Charge B, che permette"); got != "name is ChargeBee, che permette" {
		t.Errorf("got %q", got)
	}
}

func TestCasePreservation(t *testing.T) {
	d := Parse([]string{"ChargeBee"})
	if got := d.Apply("CHARGE B is great"); got != "CHARGEBEE is great" {
		t.Errorf("got %q", got)
	}
	if got := apply(t, "Seego is used"); got != "CGo is used" {
		t.Errorf("got %q", got)
	}
}

func TestAmpersandVariants(t *testing.T) {
	if got := apply(t, "send it to R and D for review"); got != "send it to R&D for review" {
		t.Errorf("got %q", got)
	}
	if got := apply(t, "send it to RD for review"); got != "send it to R&D for review" {
		t.Errorf("got %q", got)
	}
}

func TestNonASCIISkipped(t *testing.T) {
	// Non-ASCII words can never be candidates, and correction runs on every
	// language, so foreign text must pass through untouched. ASCII-spelled
	// Turkish words near dictionary terms are covered by the guards: "bunu"
	// cannot fuzzy-match "Bun" because keys ≤ 3 chars match exactly only.
	for _, text := range []string{
		"Belki de audio'a gönderin",
		"bunu da bir optimizasyon olarak bana yaz",
	} {
		if got := Parse(testLines).Apply(text); got != text {
			t.Errorf("Apply(%q) = %q, want unchanged", text, got)
		}
	}
}

func TestEmptyDict(t *testing.T) {
	var d Dict
	if got := d.Apply("hello world"); got != "hello world" {
		t.Errorf("got %q", got)
	}
	if got := Parse(nil).Apply("hello world"); got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestAliasParse(t *testing.T) {
	d := Parse([]string{"CGo: seego, see go"})
	if len(d.entries) != 1 {
		t.Fatalf("entries = %d", len(d.entries))
	}
	e := d.entries[0]
	if e.canonical != "CGo" {
		t.Errorf("canonical = %q", e.canonical)
	}
	// Keys: cgo, seego (the generated spoken key and both aliases collapse to
	// the same "seego").
	if len(e.keys) != 2 {
		t.Errorf("keys = %v", e.keys)
	}
}

func TestSpokenTerm(t *testing.T) {
	cases := []struct{ term, want string }{
		{"CGo", "seego"},       // letter C + word "Go"
		{"ANE", "ayenee"},      // all letters
		{"Zee", ""},            // plain word, no acronym segment
		{"OpenTelemetry", ""},  // plain word
		{"OpenAI", "openayeye"} , // word + trailing letters
		{"dp1751.md", ""},      // digits: no spoken form
	}
	for _, c := range cases {
		if got := spokenTerm(c.term); got != c.want {
			t.Errorf("spokenTerm(%q) = %q, want %q", c.term, got, c.want)
		}
	}
}

func TestKnownMissAmpersandRendering(t *testing.T) {
	// "A&E" (whisper's rendering of spoken A-N-E) is a documented miss with a
	// plain "ANE" entry: "ayee" vs "ayenee" is ~0.33, above the threshold —
	// deliberately NOT loosened to avoid fitting the corpus. The explicit
	// alias remains the escape hatch.
	if got := apply(t, "run it on A&E right now"); got != "run it on A&E right now" {
		t.Errorf("plain dict: got %q, want unchanged", got)
	}
	d := Parse([]string{"ANE: a&e"})
	if got := d.Apply("run it on A&E right now"); got != "run it on ANE right now" {
		t.Errorf("alias escape hatch: got %q", got)
	}
}

func TestSingleLettersInProseNotRewritten(t *testing.T) {
	// General-case probes for the spoken expansion: ordinary single letters
	// must never trigger a correction.
	for _, text := range []string{
		"plan b is better",
		"vitamin c helps a lot",
		"option a or option b",
		"the u s market opened",
	} {
		if got := apply(t, text); got != text {
			t.Errorf("Apply(%q) = %q, want unchanged", text, got)
		}
	}
}

func TestNoOpPreservesInputVerbatim(t *testing.T) {
	// When nothing matches, the input must come back byte-for-byte —
	// including newlines and spacing (cloud providers can return them).
	text := "First paragraph.\n\nSecond  paragraph here."
	if got := apply(t, text); got != text {
		t.Errorf("got %q, want verbatim input", got)
	}
}
