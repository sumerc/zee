// Package correct applies deterministic vocabulary correction to transcribed
// text: it maps misheard spans onto a user dictionary (hints.txt) using exact
// alias keys plus fuzzy matching (Levenshtein distance with a Soundex boost).
// The algorithm is a port of Handy's apply_custom_words
// (github.com/cjpais/Handy, src-tauri/src/audio_toolkit/text.rs) extended with
// alias keys ("CGo: seego, see go") so terms whose spoken form defeats
// phonetics (letter-pronounced acronyms) can still be matched exactly.
//
// Matching is ASCII/English-only by construction: keys that contain
// non-ASCII-alphanumeric characters are skipped, and the Soundex boost only
// applies to alphabetic keys. See docs/design-notes.md for why correction
// happens here instead of in the whisper prompt.
package correct

import (
	"slices"
	"strings"
	"unicode"
)

// DefaultThreshold is the accept bound for the combined fuzzy score
// (0 = exact). Handy ships the same default.
const DefaultThreshold = 0.18

// soundexBoost scales the Levenshtein score down when two keys share a
// Soundex code, so phonetically-alike words match at larger edit distances.
const soundexBoost = 0.3

// maxCandidateLen guards against pathological n-grams.
const maxCandidateLen = 50

// maxRawScore caps the pre-boost normalized edit distance: beyond ~1/3 edits
// two strings are different words no matter what Soundex says.
const maxRawScore = 0.4


type entry struct {
	canonical string
	keys      []string
}

// Dict is a parsed correction dictionary. The zero value corrects nothing.
type Dict struct {
	entries []entry
}

// Parse builds a Dict from hints lines. Each line is either a bare term
// ("OpenTelemetry") or a term with comma-separated aliases
// ("CGo: seego, see go"). Aliases become additional exact match keys for the
// canonical term. Lines whose keys are empty or non-ASCII are skipped for
// matching (they may still be valid prompt hints for cloud providers).
func Parse(lines []string) Dict {
	var d Dict
	for _, line := range lines {
		canonical := strings.TrimSpace(line)
		var aliases []string
		if before, after, found := strings.Cut(line, ":"); found {
			canonical = strings.TrimSpace(before)
			for a := range strings.SplitSeq(after, ",") {
				if a = strings.TrimSpace(a); a != "" {
					aliases = append(aliases, a)
				}
			}
		}
		if canonical == "" {
			continue
		}
		var keys []string
		add := func(text string) {
			if isFuzzyKey(text) && !containsKey(keys, text) {
				keys = append(keys, text)
			}
		}
		add(matchKey(canonical))
		// The "&"→"and" variant applies to the canonical term only ("R&D" →
		// "randd" catches spoken "R and D"). For aliases it is a trap: the
		// "aande" variant of an "a&e" alias fuzzy-matches every "and <word>"
		// bigram in normal prose.
		if strings.Contains(canonical, "&") {
			add(matchKey(strings.ReplaceAll(canonical, "&", " and ")))
		}
		// Acronym-segmented terms also match by pronunciation: "CGo" is
		// spoken "see go", "ANE" is "ay en ee". Generated automatically so
		// hints.txt stays a plain list of real terms; explicit aliases remain
		// the escape hatch for renderings the expansion cannot predict.
		add(spokenTerm(canonical))
		for _, a := range aliases {
			add(matchKey(a))
		}
		if len(keys) > 0 {
			d.entries = append(d.entries, entry{canonical: canonical, keys: keys})
		}
	}
	return d
}

// Replacement records one applied correction: the span as transcribed and the
// dictionary term it became.
type Replacement struct {
	From, To string
}

// Apply corrects text against the dictionary and returns the result. Word
// spacing is normalized to single spaces; punctuation and the case pattern of
// replaced spans are preserved.
func (d Dict) Apply(text string) string {
	out, _ := d.Correct(text)
	return out
}

// Correct is Apply plus the list of replacements made, for diagnostics.
func (d Dict) Correct(text string) (string, []Replacement) {
	if len(d.entries) == 0 {
		return text, nil
	}

	var applied []Replacement
	words := strings.Fields(text)
	result := make([]string, 0, len(words))
	for i := 0; i < len(words); {
		// Shortest span first, longer spans must score strictly better: on a
		// tie the small match wins, so "CHARGE B is" cannot swallow "is" when
		// "CHARGE B" already matches equally well. (Handy iterates longest
		// first and eats the extra word on Soundex ties.)
		bestN, bestScore, bestRepl := 0, 0.0, ""
		for n := 1; n <= 3; n++ {
			if i+n > len(words) {
				continue
			}
			span := words[i : i+n]
			// Do not consume across a punctuation boundary: in "Charge B, che"
			// the comma closes the candidate at "B,".
			if crossesPunctuation(span) {
				continue
			}
			candidate := buildNgram(span)
			repl, score, ok := d.findBest(candidate)
			// The span's pronunciation is a second candidate: single-letter
			// tokens expand to letter names ("z" → "zee") so acronyms match
			// without hand-written aliases.
			if sp := spokenSpan(span); sp != "" && sp != candidate {
				if r2, s2, ok2 := d.findBest(sp); ok2 && (!ok || s2 < score) {
					repl, score, ok = r2, s2, true
				}
			}
			if !ok {
				continue
			}
			// A span made entirely of common English words may only be
			// replaced on an exact key hit — "been" must never fuzzy-match a
			// "Bun" entry, "note is" must never fuzzy-match "Node.js". Spans
			// containing a non-common word ("did application") stay eligible.
			if score > 0 && allCommon(span) {
				continue
			}
			if bestN == 0 || score < bestScore {
				bestN, bestScore, bestRepl = n, score, repl
			}
		}
		if bestN == 0 {
			result = append(result, words[i])
			i++
			continue
		}
		prefix, _ := splitPunctuation(words[i])
		_, suffix := splitPunctuation(words[i+bestN-1])
		corrected := preserveCase(words[i], bestRepl)
		if from := strings.Join(words[i:i+bestN], " "); from != prefix+corrected+suffix {
			applied = append(applied, Replacement{From: from, To: corrected})
		}
		result = append(result, prefix+corrected+suffix)
		i += bestN
	}
	if len(applied) == 0 {
		return text, nil
	}
	return strings.Join(result, " "), applied
}

// findBest returns the canonical term with the lowest combined score below
// DefaultThreshold, if any.
func (d Dict) findBest(candidate string) (string, float64, bool) {
	if !isFuzzyKey(candidate) || len(candidate) > maxCandidateLen {
		return "", 0, false
	}
	best, bestScore := "", DefaultThreshold
	found := false
	candSoundex := soundex(candidate)
	for _, e := range d.entries {
		for _, k := range e.keys {
			// Short keys are wildcards under fuzzy matching ("ae" would
			// capture "ah"); they only ever match exactly.
			if len(k) <= 3 && candidate != k {
				continue
			}
			// Length guard: max 25% difference (at least 2 chars), so an
			// n-gram cannot swallow a much shorter term ("openaigpt" vs
			// "openai").
			maxLen := max(len(candidate), len(k))
			diff := abs(len(candidate) - len(k))
			if float64(diff) > max(float64(maxLen)*0.25, 2.0) {
				continue
			}
			score := float64(levenshtein(candidate, k)) / float64(maxLen)
			// The Soundex boost must not rescue genuinely distant strings:
			// "nowdoes" is 0.43 from "nodejs" yet shares its code. Cap the
			// raw edit distance before any boost applies.
			if score > maxRawScore {
				continue
			}
			if candSoundex != "" && candSoundex == soundex(k) {
				score *= soundexBoost
			}
			if score < bestScore {
				best, bestScore, found = e.canonical, score, true
			}
		}
	}
	return best, bestScore, found
}

func matchKey(word string) string {
	var b strings.Builder
	for _, r := range word {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// buildNgram concatenates the match keys of a word span, so "Charge B" can
// match "ChargeBee".
func buildNgram(span []string) string {
	var b strings.Builder
	for _, w := range span {
		b.WriteString(matchKey(w))
	}
	return b.String()
}

// isFuzzyKey reports whether a key is usable for matching: non-empty ASCII
// alphanumerics. Non-ASCII terms are skipped — the tokenization and Soundex
// scoring here are unsuitable for them.
func isFuzzyKey(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9') {
			return false
		}
	}
	return true
}

func containsKey(keys []string, k string) bool {
	return slices.Contains(keys, k)
}

// letterNames are the spoken English names of a–z, used to expand acronyms on
// the dictionary side and single-letter tokens on the transcript side.
var letterNames = [26]string{
	"ay", "bee", "see", "dee", "ee", "ef", "gee", "aitch", "eye", "jay",
	"kay", "el", "em", "en", "oh", "pee", "cue", "ar", "es", "tee",
	"you", "vee", "doubleyou", "ex", "why", "zee",
}

// spokenTerm builds the pronunciation key of a dictionary term with acronym
// segments: an uppercase letter NOT followed by a lowercase letter is spoken
// by name, an uppercase-initial word reads as a word. "CGo" → "seego"
// (see+go), "ANE" → "ayenee", "Zee" → "" (plain word, no acronym segment).
// Terms with digits or non-ASCII letters get no spoken key.
func spokenTerm(term string) string {
	runes := []rune(term)
	var b strings.Builder
	acronym := false
	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case r >= 'A' && r <= 'Z' && (i+1 >= len(runes) || !unicode.IsLower(runes[i+1])):
			b.WriteString(letterNames[r-'A'])
			acronym = true
			i++
		case unicode.IsLetter(r) && r < 128:
			// A word segment: initial (possibly uppercase) letter plus the
			// following lowercase run.
			b.WriteRune(unicode.ToLower(r))
			i++
			for i < len(runes) && unicode.IsLower(runes[i]) && runes[i] < 128 {
				b.WriteRune(runes[i])
				i++
			}
		default:
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return "" // digits and non-ASCII have no letter-name form
			}
			i++ // punctuation ("&", ".") separates segments
		}
	}
	if !acronym {
		return ""
	}
	return b.String()
}

// spokenSpan builds the pronunciation form of a transcript span: single-letter
// tokens and single letters around "&" expand to letter names ("z" → "zee",
// "A&E" → "ayee"); other words pass through normalized. Returns "" when the
// span has no letter to expand.
func spokenSpan(span []string) string {
	var b strings.Builder
	expanded := false
	for _, w := range span {
		for part := range strings.SplitSeq(w, "&") {
			k := matchKey(part)
			if len(k) == 1 && k[0] >= 'a' && k[0] <= 'z' {
				b.WriteString(letterNames[k[0]-'a'])
				expanded = true
			} else {
				b.WriteString(k)
			}
		}
	}
	if !expanded {
		return ""
	}
	return b.String()
}

// allCommon reports whether every word of a span is a common English word.
func allCommon(span []string) bool {
	for _, w := range span {
		if !commonWords[matchKey(w)] {
			return false
		}
	}
	return true
}

// crossesPunctuation reports whether any word before the last carries trailing
// punctuation.
func crossesPunctuation(span []string) bool {
	for _, w := range span[:len(span)-1] {
		if _, suffix := splitPunctuation(w); suffix != "" {
			return true
		}
	}
	return false
}

// splitPunctuation returns the non-alphanumeric prefix and suffix of a word.
func splitPunctuation(word string) (prefix, suffix string) {
	runes := []rune(word)
	start := 0
	for start < len(runes) && !isAlnum(runes[start]) {
		start++
	}
	end := len(runes)
	for end > start && !isAlnum(runes[end-1]) {
		end--
	}
	return string(runes[:start]), string(runes[end:])
}

func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// preserveCase applies the original word's case pattern to the replacement:
// ALL-CAPS stays all-caps, Capitalized stays capitalized, otherwise the
// dictionary's own casing wins (so "seego" → "CGo").
func preserveCase(original, replacement string) string {
	letters := []rune(original)
	hasLower := false
	hasUpper := false
	for _, r := range letters {
		if unicode.IsLower(r) {
			hasLower = true
		}
		if unicode.IsUpper(r) {
			hasUpper = true
		}
	}
	letterCount := 0
	for _, r := range letters {
		if unicode.IsLetter(r) {
			letterCount++
		}
	}
	switch {
	// A single capital letter ("Z") is not an ALL-CAPS word; the dictionary
	// casing wins there ("Zee", not "ZEE").
	case hasUpper && !hasLower && letterCount >= 2:
		return strings.ToUpper(replacement)
	case len(letters) > 0 && firstLetterUpper(original):
		r := []rune(replacement)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
		}
		return string(r)
	default:
		return replacement
	}
}

func firstLetterUpper(word string) bool {
	for _, r := range word {
		if unicode.IsLetter(r) {
			return unicode.IsUpper(r)
		}
	}
	return false
}

// soundex returns the American Soundex code of an ASCII-alphabetic key, or ""
// when the key contains digits (numeric terms get edit distance only, no
// phonetic boost).
func soundex(key string) string {
	for i := 0; i < len(key); i++ {
		if key[i] < 'a' || key[i] > 'z' {
			return ""
		}
	}
	if key == "" {
		return ""
	}
	code := func(c byte) byte {
		switch c {
		case 'b', 'f', 'p', 'v':
			return '1'
		case 'c', 'g', 'j', 'k', 'q', 's', 'x', 'z':
			return '2'
		case 'd', 't':
			return '3'
		case 'l':
			return '4'
		case 'm', 'n':
			return '5'
		case 'r':
			return '6'
		}
		return 0 // vowels, h, w, y
	}
	out := []byte{key[0] - 'a' + 'A'}
	prev := code(key[0])
	for i := 1; i < len(key) && len(out) < 4; i++ {
		c := key[i]
		d := code(c)
		if d != 0 && d != prev {
			out = append(out, d)
		}
		// h and w are transparent: a consonant on either side of them counts
		// as adjacent. Vowels reset the previous code.
		if c != 'h' && c != 'w' {
			prev = d
		}
	}
	for len(out) < 4 {
		out = append(out, '0')
	}
	return string(out)
}

// levenshtein computes edit distance between two ASCII keys.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// commonWords are frequent English words that must never be rewritten by a
// fuzzy match (exact alias hits still apply). Without this, a dictionary entry
// like "Bun" would capture "been" via the Soundex boost.
var commonWords = func() map[string]bool {
	words := strings.Fields(`
a about after again all also am an and any are as at back be because been
before being between both but by came can come could day did do does down
each even first for from get give go good got had has have he her here him
his how i if in into is it its just know like little long look made make
many may me men more most much must my never new no not now of off old on
one only or other our out over own people right said same see she should so
some still such take than that the their them then there these they thing
think this those three through time to too two under up us use very want
was way we well went were what when where which while who will with work
would year you your
also really actually maybe okay yeah yes no please thanks sorry keep kind
find found need needs using used run running start stop end open close read
write file files test tests text word words line lines part case point
place small large next last least less able every another something anything
nothing everything spend spent sound sounds around count call called
note notes easy hard ah oh
`)
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}()
