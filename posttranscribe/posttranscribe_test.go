package posttranscribe

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zee/config"
)

// last is the most recent chat request the fake server received; warms
// counts the warm-up HEAD requests.
var (
	last      request
	warms     atomic.Int32
	reply     = `{"choices":[{"message":{"role":"assistant","content":"  Hello there!  "}}]}`
	replyCode = http.StatusOK
)

func TestMain(m *testing.M) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			warms.Add(1) // TracedClient's warm-up HEAD
			return
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
	out, err := Rewrite(config.PostTranscribe{Active: "friendly"}, "Claude, JSON", "tr", "hello their")
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
	if strings.Contains(sys, "The transcript is in") {
		t.Errorf("Groq prompt names the language; only the local model gets that line:\n%s", sys)
	}
}

// TestRewriteStripsTranscriptTags: a reply that echoes the <transcript>
// wrapper is unwrapped before it is pasted.
func TestRewriteStripsTranscriptTags(t *testing.T) {
	defer func(r string) { reply = r }(reply)
	reply = `{"choices":[{"message":{"content":"<transcript>\n Hello there! \n</transcript>\n"}}]}`
	out, err := Rewrite(config.PostTranscribe{Active: "clean"}, "", "", "hello their")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Hello there!" {
		t.Errorf("out = %q, want the reply without the tags", out)
	}
}

func TestRewriteUserProfileShadowsBuiltin(t *testing.T) {
	c := config.PostTranscribe{
		Active:  "friendly",
		Model:   "llama-3.3-70b-versatile",
		Prompts: map[string]string{"friendly": "Only fix typos."},
	}
	if _, err := Rewrite(c, "", "", "x"); err != nil {
		t.Fatal(err)
	}
	sys := last.Messages[0].Content
	if !strings.Contains(sys, "Only fix typos.") || strings.Contains(sys, Builtin["friendly"]) {
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
	_, err := Rewrite(config.PostTranscribe{Active: "pirate", Prompts: map[string]string{"mine": "x"}}, "", "", "x")
	if err == nil || !strings.Contains(err.Error(), "clean, friendly, professional, mine") {
		t.Errorf("err = %v, want the list of profiles", err)
	}
}

// TestResolve: a name that is no profile acts as None; None, a built-in and a
// user prompt are kept, and the other fields always are.
func TestResolve(t *testing.T) {
	prompts := map[string]string{"mine": "x"}
	for _, tc := range []struct {
		active, want string
		ok           bool
	}{
		{"", "", true},
		{"clean", "clean", true},
		{"mine", "mine", true},
		{"frendly", "", false},
	} {
		got, ok := Resolve(config.PostTranscribe{Active: tc.active, Model: "m", Prompts: prompts})
		if got.Active != tc.want || ok != tc.ok || got.Model != "m" || got.Prompts["mine"] != "x" {
			t.Errorf("Resolve(%q) = %+v, %v; want Active %q, %v", tc.active, got, ok, tc.want, tc.ok)
		}
	}
}

func TestRewriteErrors(t *testing.T) {
	defer func(r string, c int) { reply, replyCode = r, c }(reply, replyCode)
	for _, tc := range []struct {
		name, body string
		code       int
		want       string
	}{
		{"http error", `{"error":"bad key"}`, http.StatusUnauthorized, "401"},
		{"long http error", strings.Repeat("x", 1000), http.StatusBadGateway, strings.Repeat("x", maxErrBody) + "…"},
		{"no choices", `{"choices":[]}`, http.StatusOK, "no choices"},
		{"empty text", `{"choices":[{"message":{"content":"  "}}]}`, http.StatusOK, "empty"},
	} {
		reply, replyCode = tc.body, tc.code
		_, err := Rewrite(config.PostTranscribe{Active: "friendly"}, "", "", "x")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), strings.Repeat("x", maxErrBody+1)) {
			t.Errorf("%s: error body not cut short: %d bytes", tc.name, len(err.Error()))
		}
	}
}

// TestWarm: every Warm sends exactly one warm-up, including the first, whose
// call creates the client that already warms itself.
func TestWarm(t *testing.T) {
	clientMu.Lock()
	shared = nil
	clientMu.Unlock()
	warms.Store(0)

	for want := int32(1); want <= 2; want++ {
		Warm(config.PostTranscribe{})
		// The new client's own warm-up runs in the background.
		for deadline := time.Now().Add(2 * time.Second); warms.Load() < want && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond) // room for a stray second warm-up
		if got := warms.Load(); got != want {
			t.Fatalf("after Warm #%d: %d warm-ups, want %d", want, got, want)
		}
	}
}

func TestProfiles(t *testing.T) {
	got := Profiles(config.PostTranscribe{Prompts: map[string]string{"slack": "x", "clean": "y"}})
	want := []string{"clean", "friendly", "professional", "slack"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Profiles = %q, want %q", got, want)
	}
}

// TestBuiltinOrder: every built-in is listed exactly once and described, so
// none is missing from the tray menu or its tooltip.
func TestBuiltinOrder(t *testing.T) {
	got := slices.Sorted(slices.Values(builtinOrder))
	want := slices.Sorted(maps.Keys(Builtin))
	if !slices.Equal(got, want) {
		t.Errorf("builtinOrder = %q, want the keys of Builtin %q", builtinOrder, want)
	}
	if d := slices.Sorted(maps.Keys(descriptions)); !slices.Equal(d, want) {
		t.Errorf("descriptions cover %q, want every built-in %q", d, want)
	}
}
