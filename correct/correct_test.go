package correct

import "testing"

// The dictionary used across tests mirrors a realistic hints.txt: fuzzy terms
// plus alias lines for terms whose spoken form defeats phonetics.
var testLines = []string{
	"OpenTelemetry",
	"OpenAI",
	"deduplication",
	"Zee: z",
	"Bun",
	"AppKit",
	"ChatGPT",
	"Node.js",
	"CGo: seego, see go",
	"ANE: a&e",
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
		{"the Seego appkit was done", "the CGo AppKit was done"},
		{"run it on A&E right now", "run it on ANE right now"},
		{"I run z from main branch", "I run Zee from main branch"},
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
	// Keys: cgo, seego (both "seego" and "see go" normalize to the same key).
	if len(e.keys) != 2 {
		t.Errorf("keys = %v", e.keys)
	}
}
