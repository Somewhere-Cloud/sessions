package autotitle

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

func TestCleanTitleKeepsOnlyABareTitle(t *testing.T) {
	cases := map[string]string{
		"Fix flaky login test":                       "Fix flaky login test",
		"  \n\"Fix flaky login test.\"\nExplanation": "Fix flaky login test",
		"Title: **Database migration**":              "Database migration",
		"# Rate tables!":                             "Rate tables",
		"`Ship the release`":                         "Ship the release",
		"“Curly quotes”":                             "Curly quotes",
		"Tabs\tand   spaces":                         "Tabs and spaces",
		strings.Repeat("word ", 30):                  "",
	}
	for raw, want := range cases {
		got, err := cleanTitle(raw)
		if want == "" {
			if err != nil || utf8.RuneCountInString(got) > maxTitleRunes || !strings.HasSuffix(got, "…") {
				t.Fatalf("cleanTitle(long) = %q, %v", got, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Fatalf("cleanTitle(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, empty := range []string{"", "  \n ", "\"\"", "Title:"} {
		if got, err := cleanTitle(empty); err == nil {
			t.Fatalf("cleanTitle(%q) = %q, want an error", empty, got)
		}
	}
}

func TestPromptBoundsEachSideOfTheExchange(t *testing.T) {
	long := strings.Repeat("é", maxExcerptBytes)
	exchange := trimExchange(Exchange{FirstMessage: long, Reply: "short"})
	if len(exchange.FirstMessage) > maxExcerptBytes+len("…") || !utf8.ValidString(exchange.FirstMessage) {
		t.Fatalf("excerpt is %d bytes, valid=%v", len(exchange.FirstMessage), utf8.ValidString(exchange.FirstMessage))
	}
	text := prompt(exchange)
	if !strings.Contains(text, "FIRST_REQUEST") || !strings.Contains(text, "ASSISTANT_REPLY\nshort") {
		t.Fatalf("prompt = %q", text)
	}
}

func TestNeedsTitleOnlyForUnnamedProviderConversations(t *testing.T) {
	cases := []struct {
		info state.SessionInfo
		want bool
	}{
		{state.SessionInfo{Tool: state.ToolCodex}, true},
		{state.SessionInfo{Tool: state.ToolClaude, NameSource: state.NameSourceLaunch}, true},
		{state.SessionInfo{Tool: state.ToolCodex, Name: "Chosen"}, false},
		{state.SessionInfo{Tool: state.ToolCodex, Name: "Mine", NameSource: state.NameSourceExplicit}, false},
		{state.SessionInfo{Tool: state.ToolCodex, Name: "Old", NameSource: state.NameSourceGenerated}, false},
		{state.SessionInfo{Tool: state.ToolClaude, ClaudeAITitle: "Claude's"}, false},
		{state.SessionInfo{Tool: state.ToolTerminal}, false},
		{state.SessionInfo{Tool: state.ToolLane}, false},
	}
	for _, test := range cases {
		if got := needsTitle(test.info); got != test.want {
			t.Fatalf("needsTitle(%+v) = %v, want %v", test.info, got, test.want)
		}
	}
}
