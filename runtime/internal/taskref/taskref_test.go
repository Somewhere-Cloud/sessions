package taskref

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAcceptsOnlyTheTwoCanonicalForms(t *testing.T) {
	for _, test := range []struct {
		raw, project, canonical, provider, scope string
	}{
		{"sw:tsk_0123abcd", "", "sw:tsk_0123abcd", ProviderSomewhere, ScopeUnresolved},
		{"  SW:TSK_0123ABCDEF  ", "", "sw:tsk_0123abcdef", ProviderSomewhere, ScopeUnresolved},
		{"sw:tsk_0123abcd", "my-project", "sw:tsk_0123abcd", ProviderSomewhere, "somewhere:my-project"},
		{"gh:Acme/App#12", "", "gh:acme/app#12", ProviderGitHub, "github.com/acme/app"},
		{"gh:acme/app.js#1", "", "gh:acme/app.js#1", ProviderGitHub, "github.com/acme/app.js"},
	} {
		ref, err := Parse(test.raw, test.project)
		if err != nil {
			t.Fatalf("Parse(%q): %v", test.raw, err)
		}
		if ref.Canonical != test.canonical || ref.Provider != test.provider || ref.Scope != test.scope {
			t.Fatalf("Parse(%q) = %+v", test.raw, ref)
		}
	}
}

func TestParseRefusesEverythingElse(t *testing.T) {
	for _, test := range []struct{ raw, project string }{
		{"", ""}, {"tsk_0123abcd", ""}, {"sw:0123abcd", ""}, {"sw:tsk_0123", ""}, {"sw:tsk_xyz12345", ""},
		{"sw:tsk_" + strings.Repeat("a", 65), ""}, {"jira:ABC-1", ""}, {"gh:acme/app", ""}, {"gh:acme#1", ""},
		{"gh:acme/app#0", ""}, {"gh:acme/app#01", ""}, {"gh:acme/app#1234567890", ""}, {"gh:-acme/app#1", ""},
		{"gh:acme/app.git#1", ""}, {"gh:acme/..#1", ""}, {"gh:acme/app#1x", ""}, {"https://github.com/acme/app/issues/1", ""},
		{"gh:acme/app#1", "my-project"}, {"sw:tsk_0123abcd", "bad project"}, {"sw:tsk_0123abcd", "-lead"},
		{"feature/tsk_0123abcd-fix", ""},
	} {
		if _, err := Parse(test.raw, test.project); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Parse(%q, %q) err = %v, want ErrInvalid", test.raw, test.project, err)
		}
	}
}

func TestParseAllDedupesScopesAndBounds(t *testing.T) {
	refs, err := ParseAll([]string{"sw:tsk_0123abcd", "gh:acme/app#1", "SW:tsk_0123ABCD"}, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].Project != "proj" || refs[1].Project != "" {
		t.Fatalf("refs = %+v", refs)
	}
	if _, err := ParseAll([]string{"sw:tsk_0123abcd", "nope"}, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a partly invalid list was accepted: %v", err)
	}
	many := make([]string, MaxPerTarget+1)
	for index := range many {
		many[index] = "gh:acme/app#1"
	}
	if _, err := ParseAll(many, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unbounded list was accepted: %v", err)
	}
}
