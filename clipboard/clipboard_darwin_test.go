package clipboard

import "testing"

// Round-trips through the real pasteboard, restoring whatever was there. The
// Turkish characters are the point: the pbcopy backend mangled them unless
// LC_CTYPE was forced to UTF-8, and NSPasteboard takes an NSString directly, so
// the locale hack could go.
func TestCopyReadRoundTrip(t *testing.T) {
	prev, err := Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	t.Cleanup(func() { Copy(prev) })

	const want = "zee ğşıİçöü — naïve 日本語"
	if err := Copy(want); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	got, err := Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != want {
		t.Errorf("round trip: got %q, want %q", got, want)
	}
}

func BenchmarkCopy(b *testing.B) {
	prev, _ := Read()
	b.Cleanup(func() { Copy(prev) })
	for b.Loop() {
		Copy("the quick brown fox jumps over the lazy dog")
	}
}
