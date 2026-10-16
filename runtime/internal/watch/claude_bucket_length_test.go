package watch

import (
	"strings"
	"testing"
)

// The provider keeps only the first 200 characters of an encoded path and then
// appends a base-36 hash of the original, so two long directories sharing a
// prefix still land in different buckets. Sessions encoded the whole thing, so
// a deeply nested workspace resolved to a name neither side could agree on --
// the conversation was not merely somewhere else, it was nowhere findable.
//
// These fixed vectors use the provider's 2.1.222 encoding algorithm. The
// synthetic home-path vectors were recalculated with a separate JavaScript
// UTF-16 implementation, not this Go encoder.
func TestStrictEncodingMatchesTheProviderPastItsLengthLimit(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		cwd   string
		want  string
		short bool
	}{
		{
			name:  "a path at the limit keeps every character",
			cwd:   "/" + strings.Repeat("z", 199),
			want:  "-" + strings.Repeat("z", 199),
			short: true,
		},
		{
			// One character longer, and the whole shape changes.
			name: "a path one past the limit is cut and hashed",
			cwd:  "/" + strings.Repeat("z", 200),
			want: "-" + strings.Repeat("z", 199) + "-6zubpt",
		},
		{
			name: "a long ordinary path",
			cwd:  "/Users/example/" + strings.Repeat("a", 300),
			want: "-Users-example-" + strings.Repeat("a", 185) + "-r01htn",
		},
		{
			// The hash runs over UTF-16 code units, so an astral character
			// contributes two surrogates. Iterating runes instead would give a
			// different hash and a different directory.
			name: "a long path containing an astral character",
			cwd:  "/Users/example/\U0001F600emoji/" + strings.Repeat("y", 240),
			want: "-Users-example---emoji-" + strings.Repeat("y", 177) + "-8l96vb",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := encodeClaudePathStrict(testCase.cwd)
			if got != testCase.want {
				t.Fatalf("encoded to a bucket the provider does not use\n got %q\nwant %q", got, testCase.want)
			}
			if testCase.short && len(got) > claudeProjectDirMaxLength {
				t.Fatalf("a path within the limit was truncated: %d characters", len(got))
			}
		})
	}
}

// Two workspaces sharing their first 200 characters must not share a bucket.
// That is the whole reason the provider hashes rather than simply truncating.
func TestLongPathsSharingAPrefixGetDifferentBuckets(t *testing.T) {
	prefix := "/Users/example/" + strings.Repeat("p", 260)
	first := encodeClaudePathStrict(prefix + "/one")
	second := encodeClaudePathStrict(prefix + "/two")
	if first == second {
		t.Fatalf("two workspaces collapsed into one bucket: %q", first)
	}
	if !strings.HasPrefix(first, "-Users-example-"+strings.Repeat("p", 100)) {
		t.Fatalf("truncated bucket lost its prefix: %q", first)
	}
}
