package posttranscribe

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"zee/config"
)

// last is the most recent chat request the fake server received.
var (
	last      request
	reply     = `{"choices":[{"message":{"role":"assistant","content":"  Hello there!  "}}]}`
	replyCode = http.StatusOK
)

func TestMain(m *testing.M) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			return // TracedClient's warm-up HEAD
		}
		last = request{}
		json.NewDecoder(r.Body).Decode(&last)
		w.WriteHeader(replyCode)
		w.Write([]byte(reply))
	}))
	defer srv.Close()
	apiURL = srv.URL + "/openai/v1/chat/completions"

	dir, _ := os.MkdirTemp("", "zee-posttranscribe")
	defer os.RemoveAll(dir)
	config.SetDir(dir)
	config.SetAPIKey(Provider, "test-key")

	os.Exit(m.Run())
}

func TestRewriteBuiltin(t *testing.T) {
	out, err := Rewrite(config.PostTranscribe{Active: "friendly"}, "Claude, JSON", "hello their")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hello there!" {
		t.Errorf("out = %q, want trimmed reply", out)
	}
	if last.Model != DefaultModel || last.ReasoningEffort != "low" {
		t.Errorf("model=%q effort=%q, want %q low", last.Model, last.ReasoningEffort, DefaultModel)
	}
	sys, user := last.Messages[0].Content, last.Messages[1].Content
	if !strings.Contains(sys, Builtin["friendly"]) || !strings.Contains(sys, "Claude, JSON") {
		t.Errorf("system prompt lacks profile or vocabulary:\n%s", sys)
	}
	if !strings.Contains(user, "<transcript>\nhello their\n</transcript>") {
		t.Errorf("user message = %q", user)
	}
}

func TestRewriteUserProfileShadowsBuiltin(t *testing.T) {
	c := config.PostTranscribe{
		Active:  "grammar",
		Model:   "llama-3.3-70b-versatile",
		Prompts: map[string]string{"grammar": "Only fix typos."},
	}
	if _, err := Rewrite(c, "", "x"); err != nil {
		t.Fatal(err)
	}
	sys := last.Messages[0].Content
	if !strings.Contains(sys, "Only fix typos.") || strings.Contains(sys, Builtin["grammar"]) {
		t.Errorf("user prompt did not replace the built-in:\n%s", sys)
	}
	if strings.Contains(sys, "Vocabulary") {
		t.Error("empty hints should add no vocabulary section")
	}
	if last.Model != c.Model || last.ReasoningEffort != "" {
		t.Errorf("model=%q effort=%q, want %q with no reasoning_effort", last.Model, last.ReasoningEffort, c.Model)
	}
}

func TestRewriteUnknownProfile(t *testing.T) {
	_, err := Rewrite(config.PostTranscribe{Active: "pirate", Prompts: map[string]string{"mine": "x"}}, "", "x")
	if err == nil || !strings.Contains(err.Error(), "grammar, friendly, professional, concise, mine") {
		t.Errorf("err = %v, want the list of profiles", err)
	}
}

func TestRewriteErrors(t *testing.T) {
	defer func() { reply, replyCode = `{"choices":[{"message":{"content":"ok"}}]}`, http.StatusOK }()
	for _, tc := range []struct {
		name, body string
		code       int
		want       string
	}{
		{"http error", `{"error":"bad key"}`, http.StatusUnauthorized, "401"},
		{"no choices", `{"choices":[]}`, http.StatusOK, "no choices"},
		{"empty text", `{"choices":[{"message":{"content":"  "}}]}`, http.StatusOK, "empty"},
	} {
		reply, replyCode = tc.body, tc.code
		_, err := Rewrite(config.PostTranscribe{Active: "grammar"}, "", "x")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestProfiles(t *testing.T) {
	got := Profiles(config.PostTranscribe{Prompts: map[string]string{"slack": "x", "grammar": "y"}})
	want := []string{"grammar", "friendly", "professional", "concise", "slack"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Profiles = %q, want %q", got, want)
	}
}

// TestBuiltinOrder: every built-in is listed exactly once, so none is missing
// from the tray menu or shares a tag colour.
func TestBuiltinOrder(t *testing.T) {
	got := slices.Sorted(slices.Values(builtinOrder))
	want := slices.Sorted(maps.Keys(Builtin))
	if !slices.Equal(got, want) {
		t.Errorf("builtinOrder = %q, want the keys of Builtin %q", builtinOrder, want)
	}
}
