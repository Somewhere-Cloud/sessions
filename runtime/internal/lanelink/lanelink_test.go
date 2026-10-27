package lanelink

import "testing"

const (
	session = "31000000-0000-4000-8000-0000000000a1"
	machine = "9b2f0c3e-1d4a-4e5b-8c6d-7e8f9a0b1c2d"
)

func TestLinkFormatsLaneAndOptionalMachine(t *testing.T) {
	cases := []struct {
		session, machine, want string
	}{
		{session, "", "sessions://lane/" + session},
		{session, machine, "sessions://lane/" + session + "?machine=" + machine},
		{" " + session + " ", "not-a-machine", "sessions://lane/" + session},
		{"31000000-0000-4000-8000-0000000000A1", "", "sessions://lane/" + session},
		{"", machine, ""},
		{"31000000", "", ""},
		{"../../etc/passwd", "", ""},
		{"31000000-0000-4000-8000-0000000000g1", "", ""},
	}
	for _, test := range cases {
		if got := Link(test.session, test.machine); got != test.want {
			t.Errorf("Link(%q, %q) = %q, want %q", test.session, test.machine, got, test.want)
		}
	}
}

func TestParseAcceptsOnlyLaneLinksWithUUIDs(t *testing.T) {
	for _, raw := range []string{Link(session, ""), Link(session, machine)} {
		gotSession, gotMachine, ok := Parse(raw)
		if !ok || gotSession != session {
			t.Fatalf("Parse(%q) = %q, %q, %v", raw, gotSession, gotMachine, ok)
		}
		if raw == Link(session, machine) && gotMachine != machine {
			t.Fatalf("Parse(%q) machine = %q", raw, gotMachine)
		}
	}
	for _, raw := range []string{
		"sessions://pair?host=x&t=y",
		"https://lane/" + session,
		"sessions://lane/" + session + "/extra",
		"sessions://lane/" + session + "?machine=bad",
		"sessions://lane/" + session + "?machine=" + machine + "&machine=" + machine,
		"sessions://lane/" + session + "?run=rm",
		"sessions://lane/" + session + "#x",
		"sessions://user@lane/" + session,
		"sessions://lane/",
	} {
		if _, _, ok := Parse(raw); ok {
			t.Errorf("Parse(%q) accepted a link it must refuse", raw)
		}
	}
}
