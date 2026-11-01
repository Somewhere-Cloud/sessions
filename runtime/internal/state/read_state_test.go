package state

import (
	"errors"
	"path/filepath"
	"testing"
)

func millis(value int64) *int64 { return &value }

func TestSessionUnreadRule(t *testing.T) {
	cases := []struct {
		name string
		info SessionInfo
		want bool
	}{
		{"nothing recorded", SessionInfo{}, false},
		{"attention after seen", SessionInfo{SeenAt: millis(10), AttentionAt: millis(20)}, true},
		{"attention before seen", SessionInfo{SeenAt: millis(30), AttentionAt: millis(20)}, false},
		// Typing into a session is looking at it.
		{"human message after attention", SessionInfo{SeenAt: millis(10), AttentionAt: millis(20), LastHumanMessageAt: millis(25)}, false},
		{"reply after human message", SessionInfo{AttentionAt: millis(30), LastHumanMessageAt: millis(25)}, true},
		// An agent's own child nobody followed stays calm.
		{"never seen or spoken into", SessionInfo{AttentionAt: millis(30)}, false},
		{"explicit mark", SessionInfo{MarkedUnread: true}, true},
		{"ended is never unread", SessionInfo{Exited: true, MarkedUnread: true, SeenAt: millis(1), AttentionAt: millis(2)}, false},
	}
	for _, test := range cases {
		if got := SessionUnread(test.info); got != test.want {
			t.Errorf("%s: SessionUnread = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestReadStatePersistsAndSurvivesARunnerWrite(t *testing.T) {
	root := t.TempDir()
	runnerDir := filepath.Join(root, "runners")
	if err := EnsureDir(runnerDir); err != nil {
		t.Fatal(err)
	}
	const id = "cccccccc-dddd-4eee-8fff-0000000000a1"
	path := filepath.Join(runnerDir, id+".json")
	if err := WriteMetadata(path, Metadata{ID: id, Cmd: "/bin/sh", Cwd: root, Name: "bolo"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(Config{RunnerStateDir: runnerDir}, nil)

	seen, err := registry.UpdateReadState(id, false)
	if err != nil || seen.Unread || seen.SeenAt == nil {
		t.Fatalf("UpdateReadState(false) = %+v, %v", seen, err)
	}
	if err := registry.RecordAttention(id, *seen.SeenAt+1); err != nil {
		t.Fatal(err)
	}
	marked, err := registry.UpdateReadState(id, true)
	if err != nil || !marked.Unread || marked.AttentionAt == nil {
		t.Fatalf("UpdateReadState(true) = %+v, %v", marked, err)
	}
	// A runner rewrites its own document with no idea these fields exist.
	if err := WriteRunnerMetadata(path, Metadata{ID: id, Cmd: "/bin/sh", Cwd: root}); err != nil {
		t.Fatal(err)
	}
	stored, err := readRunnerMetadata(path)
	if err != nil {
		t.Fatal(err)
	}
	if stored.SeenAt == nil || *stored.SeenAt != *seen.SeenAt || stored.AttentionAt == nil || !stored.MarkedUnread {
		t.Fatalf("a runner write dropped daemon-owned read state: %+v", stored)
	}
	// An older attention stamp never moves the marker backwards.
	if err := registry.RecordAttention(id, 1); err != nil {
		t.Fatal(err)
	}
	stored, _ = readRunnerMetadata(path)
	if *stored.AttentionAt != *seen.SeenAt+1 {
		t.Fatalf("attention moved backwards to %d", *stored.AttentionAt)
	}
	cleared, err := registry.UpdateReadState(id, false)
	if err != nil || cleared.Unread {
		t.Fatalf("seen did not clear the explicit mark: %+v, %v", cleared, err)
	}
}

func TestReadStateMetadataSurvivesIntoSessionInfo(t *testing.T) {
	const id = "cccccccc-dddd-4eee-8fff-0000000000a2"
	encoded := []byte(`{"id":"` + id + `","cmd":"/bin/sh","cwd":"/tmp","seen_at":5,"attention_at":9,"marked_unread":true}`)
	metadata, err := parseRunnerMetadata(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.SeenAt == nil || *metadata.SeenAt != 5 || metadata.AttentionAt == nil || *metadata.AttentionAt != 9 || !metadata.MarkedUnread {
		t.Fatalf("parsed read state = %+v", metadata)
	}
}

func TestReadStateReportsAnUnknownSessionAsNotFound(t *testing.T) {
	registry := NewRegistry(Config{RunnerStateDir: t.TempDir()}, nil)
	for _, id := range []string{"cccccccc-dddd-4eee-8fff-0000000000a3", "", "..", "../escape"} {
		if _, err := registry.UpdateReadState(id, false); !errors.Is(err, ErrSessionNotFound) {
			t.Errorf("UpdateReadState(%q) error = %v, want ErrSessionNotFound", id, err)
		}
	}
}
