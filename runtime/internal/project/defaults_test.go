package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func claudeMachine(provider string) Defaults {
	if provider != "claude" {
		return Defaults{}
	}
	return Defaults{Model: "sonnet", Effort: "medium"}
}

func TestResolveLaunchPrefersExplicitThenProjectThenMachineThenBuiltin(t *testing.T) {
	project := Defaults{Provider: "claude", Model: "opus", Worktree: "on", Permissions: "ask"}

	got := ResolveLaunch(Defaults{}, project, claudeMachine)
	want := Launch{
		Provider:    Choice{"claude", SourceProject},
		Model:       Choice{"opus", SourceProject},
		Effort:      Choice{"medium", SourceMachine},
		Worktree:    Choice{"on", SourceProject},
		Permissions: Choice{"ask", SourceProject},
	}
	if got != want {
		t.Fatalf("project over machine:\n got %#v\nwant %#v", got, want)
	}

	got = ResolveLaunch(Defaults{Model: "haiku", Permissions: "bypass", Worktree: "off"}, project, claudeMachine)
	if got.Model != (Choice{"haiku", SourceExplicit}) || got.Permissions != (Choice{"bypass", SourceExplicit}) || got.Worktree != (Choice{"off", SourceExplicit}) {
		t.Fatalf("explicit must win: %#v", got)
	}

	got = ResolveLaunch(Defaults{}, Defaults{}, claudeMachine)
	if got.Provider != (Choice{"", SourceBuiltin}) || got.Worktree != (Choice{BuiltinWorktree, SourceBuiltin}) || got.Permissions != (Choice{BuiltinPermissions, SourceBuiltin}) || got.Model.Source != SourceBuiltin {
		t.Fatalf("built-in fallbacks: %#v", got)
	}
	got = ResolveLaunch(Defaults{Provider: "claude"}, Defaults{}, claudeMachine)
	if got.Model != (Choice{"sonnet", SourceMachine}) {
		t.Fatalf("machine default applies with no project default: %#v", got.Model)
	}
}

// A project's model belongs to its provider. Starting the other provider on
// purpose must not inherit a model that provider cannot run.
func TestResolveLaunchDropsProjectModelForAnExplicitlyDifferentProvider(t *testing.T) {
	project := Defaults{Provider: "claude", Model: "opus", Effort: "max", Worktree: "on"}
	got := ResolveLaunch(Defaults{Provider: "codex"}, project, claudeMachine)
	if got.Provider != (Choice{"codex", SourceExplicit}) || got.Model.Value != "" || got.Effort.Value != "" {
		t.Fatalf("codex launch took Claude's model: %#v", got)
	}
	if got.Worktree != (Choice{"on", SourceProject}) {
		t.Fatalf("provider-independent defaults still apply: %#v", got.Worktree)
	}
	providerless := ResolveLaunch(Defaults{Provider: "codex"}, Defaults{Model: "gpt-5"}, nil)
	if providerless.Model != (Choice{"gpt-5", SourceProject}) {
		t.Fatalf("a model with no project provider applies to any provider: %#v", providerless.Model)
	}
}

func TestUpdateDefaultsSetsClearsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	store := NewStore(path, nil)
	identity := "git:github.com/o/r"
	if _, err := store.Upsert(Project{Name: "Kept", Roots: []string{canonical(t.TempDir())}}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.UpdateDefaults(identity, Defaults{Provider: "Codex", Model: "gpt-5", Effort: "HIGH", Worktree: "on", Permissions: "constrained"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if saved != (Defaults{Provider: "codex", Model: "gpt-5", Effort: "high", Worktree: "on", Permissions: "ask"}) {
		t.Fatalf("normalized = %#v", saved)
	}
	saved, err = store.UpdateDefaults(identity, Defaults{Model: "gpt-5.1"}, []string{"worktree", "effort"})
	if err != nil {
		t.Fatal(err)
	}
	if saved != (Defaults{Provider: "codex", Model: "gpt-5.1", Permissions: "ask"}) {
		t.Fatalf("after clear = %#v", saved)
	}

	reopened := NewStore(path, nil)
	loaded, err := reopened.DefaultsFor(identity)
	if err != nil || loaded != saved {
		t.Fatalf("persisted = %#v, %v", loaded, err)
	}
	if projects, _ := reopened.List(); len(projects) != 1 || projects[0].Name != "Kept" {
		t.Fatalf("writing defaults must keep stored projects: %#v", projects)
	}
	if _, err := reopened.Upsert(Project{Name: "Second", Roots: []string{canonical(t.TempDir())}}); err != nil {
		t.Fatal(err)
	}
	if loaded, _ := NewStore(path, nil).DefaultsFor(identity); loaded != saved {
		t.Fatalf("writing projects must keep defaults: %#v", loaded)
	}

	cleared, err := reopened.UpdateDefaults(identity, Defaults{}, []string{"all"})
	if err != nil || !cleared.IsZero() {
		t.Fatalf("clear all = %#v, %v", cleared, err)
	}
	encoded, _ := os.ReadFile(path)
	if strings.Contains(string(encoded), identity) {
		t.Fatalf("an emptied entry must be removed:\n%s", encoded)
	}
}

func TestUpdateDefaultsRejectsUnknownValues(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "projects.json"), nil)
	for _, bad := range []Defaults{{Provider: "gemini"}, {Worktree: "maybe"}, {Permissions: "plan"}, {Effort: "huge"}, {Model: "two words"}} {
		if _, err := store.UpdateDefaults("name:x", bad, nil); err == nil {
			t.Errorf("accepted %#v", bad)
		}
	}
	if _, err := store.UpdateDefaults("name:x", Defaults{}, []string{"colour"}); err == nil {
		t.Error("accepted an unknown --clear key")
	}
	if _, err := store.UpdateDefaults("", Defaults{Provider: "claude"}, nil); err == nil {
		t.Error("accepted an empty identity")
	}
}
