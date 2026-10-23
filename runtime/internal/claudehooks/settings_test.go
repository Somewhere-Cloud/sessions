package claudehooks

import (
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

const testLaunchID = "0123456789abcdef0123456789abcdef"

func TestShellQuotePreservesApostrophe(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("POSIX shell unavailable")
	}
	value := "/tmp/it's a path/runner"
	output, err := exec.Command(sh, "-c", "printf '%s' "+shellQuote(value)).CombinedOutput()
	if err != nil || string(output) != value {
		t.Fatalf("shell quote changed path: %q, %v", output, err)
	}
}

func TestInstallBeforeArgumentSeparator(t *testing.T) {
	args := []string{"--model", "test", "--", "literal prompt", "--settings", "not settings"}
	installed, skipped := Install(args, "/bin/runner", testLaunch())
	if skipped != "" {
		t.Fatal(skipped)
	}
	separator := slices.Index(installed, "--")
	if separator < 0 || !slices.Equal(installed[separator:], args[2:]) {
		t.Fatalf("changed literal arguments: %q", installed)
	}
	if launch, ok := FromArgs(installed); !ok || launch != testLaunch() {
		t.Fatalf("hooks not installed before separator: %q", installed)
	}
}

func TestInstallDoesNotRepairMalformedSettings(t *testing.T) {
	for _, raw := range []string{`{} {"disableAllHooks":true}`, `{} trailing`} {
		args := []string{"--settings", raw}
		installed, skipped := Install(args, "/bin/runner", testLaunch())
		if skipped != SkipUnreadableSetting || !slices.Equal(installed, args) {
			t.Fatalf("rewrote invalid user settings: %q (%s)", installed, skipped)
		}
	}
}

func testLaunch() Launch {
	return Launch{Sink: "/tmp/state dir/it's/abc" + SinkSuffix, Launch: testLaunchID}
}

func decodeSettings(t *testing.T, args []string) map[string]any {
	t.Helper()
	positions := settingsPositions(args)
	if len(positions) != 1 {
		t.Fatalf("want exactly one --settings, got %d in %q", len(positions), args)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(positions[0].value(args)), &settings); err != nil {
		t.Fatal(err)
	}
	return settings
}

func managedCount(t *testing.T, settings map[string]any, event string) int {
	t.Helper()
	hooks, _ := settings["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	count := 0
	for _, group := range groups {
		if _, managed := managedLaunchInGroup(group); managed {
			count++
		}
	}
	return count
}

// A launch with no --settings gets one, holding an async observation hook for
// every observed event, and the identity reads back from argv exactly,
// including a sink path with a space and a quote.
func TestInstallAddsAsyncHooksAndRoundTrips(t *testing.T) {
	args := []string{"--session-id", "11111111-1111-4111-8111-111111111111", "--permission-mode", "default"}
	installed, skipped := Install(args, "/opt/Sessions App/sessions-runner", testLaunch())
	if skipped != "" {
		t.Fatalf("skipped: %s", skipped)
	}
	if !slices.Equal(installed[:len(args)], args) {
		t.Fatalf("caller arguments changed: %q", installed)
	}
	settings := decodeSettings(t, installed)
	hooks := settings["hooks"].(map[string]any)
	for _, event := range Events {
		groups := hooks[event].([]any)
		hook := groups[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
		if hook["async"] != true || hook["type"] != "command" {
			t.Fatalf("%s hook is not an async command: %v", event, hook)
		}
	}
	launch, ok := FromArgs(installed)
	if !ok || launch != testLaunch() {
		t.Fatalf("FromArgs = %+v, %v", launch, ok)
	}
}

// Sessions' own Remote Control default and the caller's inline settings and
// hooks are kept; the managed hook is added beside them, never replacing them.
func TestInstallPreservesInlineSettingsAndHooks(t *testing.T) {
	user := `{"remoteControlAtStartup":false,"permissions":{"allow":["Bash(ls)"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`
	args := []string{"--remote-control-session-name-prefix", "x", "--settings", user, "--", "--settings", "literal"}
	installed, skipped := Install(args, "/bin/runner", testLaunch())
	if skipped != "" {
		t.Fatalf("skipped: %s", skipped)
	}
	if installed[len(installed)-1] != "literal" || installed[len(installed)-2] != "--settings" {
		t.Fatalf("arguments after -- changed: %q", installed)
	}
	settings := decodeSettings(t, installed)
	if settings["remoteControlAtStartup"] != false {
		t.Fatalf("Remote Control setting lost: %v", settings)
	}
	if allow := settings["permissions"].(map[string]any)["allow"].([]any); len(allow) != 1 || allow[0] != "Bash(ls)" {
		t.Fatalf("permission policy changed: %v", allow)
	}
	stop := settings["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 || !strings.Contains(stop[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string), "say done") {
		t.Fatalf("user Stop hook not preserved first: %v", stop)
	}
}

// Relaunching from argv that already carries an older launch's hooks replaces
// that launch, so only one capture runs and it carries the new launch id.
func TestInstallReplacesAnOlderManagedLaunch(t *testing.T) {
	first, _ := Install(nil, "/bin/runner", testLaunch())
	next := Launch{Sink: "/tmp/new" + SinkSuffix, Launch: strings.Repeat("f", 32)}
	second, skipped := Install(first, "/bin/runner", next)
	if skipped != "" {
		t.Fatalf("skipped: %s", skipped)
	}
	settings := decodeSettings(t, second)
	for _, event := range Events {
		if count := managedCount(t, settings, event); count != 1 {
			t.Fatalf("%s has %d managed hooks", event, count)
		}
	}
	if launch, _ := FromArgs(second); launch != next {
		t.Fatalf("FromArgs = %+v, want %+v", launch, next)
	}
}

// Anything Sessions cannot extend safely is left exactly as given, and the
// reason reads back from argv for the observation.
func TestInstallSkipsWhatItCannotExtend(t *testing.T) {
	cases := []struct {
		args   []string
		reason string
	}{
		{[]string{"--bare"}, SkipBareMode},
		{[]string{"--settings", "/home/me/settings.json"}, SkipSettingsFile},
		{[]string{"--settings={}", "--settings", "{}"}, SkipMultipleSettings},
		{[]string{"--settings", `{"disableAllHooks":true}`}, SkipHooksDisabled},
		{[]string{"--settings", `{"hooks":[]}`}, SkipUnreadableSetting},
		{[]string{"--settings", `{not json`}, SkipUnreadableSetting},
	}
	for _, test := range cases {
		installed, skipped := Install(test.args, "/bin/runner", testLaunch())
		if skipped != test.reason || !slices.Equal(installed, test.args) {
			t.Fatalf("%q: got %q %q", test.args, skipped, installed)
		}
		if _, ok := FromArgs(installed); ok {
			t.Fatalf("%q: FromArgs found a launch", test.args)
		}
	}
	if _, skipped := Install(nil, "relative/runner", testLaunch()); skipped != SkipNoRunner {
		t.Fatalf("relative runner: %q", skipped)
	}
	if reason := UnavailableReason([]string{"--settings", "/x.json"}); reason != SkipSettingsFile {
		t.Fatalf("UnavailableReason = %q", reason)
	}
	if reason := UnavailableReason([]string{"--session-id", "x"}); reason != SkipNotInstalled {
		t.Fatalf("UnavailableReason = %q", reason)
	}
}

// A user hook that shares a group with an older Sessions capture keeps its
// place, its group's matcher and other fields, and its order; only the
// Sessions entry is removed. A group that held only Sessions' entry is
// dropped, and a group shape Sessions does not recognize is kept as is.
func TestInstallKeepsUserEntriesInMixedGroups(t *testing.T) {
	old := Command("/bin/runner", testLaunch())
	user := `{"hooks":{"PreToolUse":[` +
		`{"matcher":"Bash","note":"mine","hooks":[{"type":"command","command":"audit-a"},{"type":"command","command":` + quoteJSON(old) + `,"async":true},{"type":"command","command":"audit-b","timeout":3}]},` +
		`{"hooks":[{"type":"command","command":` + quoteJSON(old) + `}]},` +
		`"unrecognized"]}}`
	next := Launch{Sink: "/tmp/next" + SinkSuffix, Launch: strings.Repeat("e", 32)}
	installed, skipped := Install([]string{"--settings", user}, "/bin/runner", next)
	if skipped != "" {
		t.Fatalf("skipped: %s", skipped)
	}
	groups := decodeSettings(t, installed)["hooks"].(map[string]any)["PreToolUse"].([]any)
	if len(groups) != 3 {
		t.Fatalf("want user group, unrecognized value, new Sessions group; got %v", groups)
	}
	mixed := groups[0].(map[string]any)
	entries := mixed["hooks"].([]any)
	if mixed["matcher"] != "Bash" || mixed["note"] != "mine" || len(entries) != 2 ||
		entries[0].(map[string]any)["command"] != "audit-a" || entries[1].(map[string]any)["command"] != "audit-b" ||
		entries[1].(map[string]any)["timeout"] != float64(3) {
		t.Fatalf("user group changed: %v", mixed)
	}
	if groups[1] != "unrecognized" {
		t.Fatalf("unrecognized group value changed: %v", groups[1])
	}
	if launch, ok := managedLaunchInGroup(groups[2]); !ok || launch != next {
		t.Fatalf("new Sessions group = %v", groups[2])
	}
	if strings.Contains(strings.Join(installed, " "), testLaunchID) {
		t.Fatal("the older launch id survived")
	}
}

func quoteJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
