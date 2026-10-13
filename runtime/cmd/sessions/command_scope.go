package main

import (
	"regexp"
	"strings"
)

// A daemon writes the instructional commands it returns (`sessions resume ID`,
// `sessions kill ID`) relative to itself: it cannot know the alias or address
// this client used to reach it. Printed bare, such a command reached through
// --machine or --host runs against this computer's daemon instead. The client
// therefore carries the global target the user typed into every command it
// passes on.

// commandScope returns the global target flags the user typed (--machine,
// --host, --port, --direct), in order and as shell words, or "" when the
// command used the default target. --json is output format, not target, and
// is left for the person to choose. A target set by SESSIONS_HOST or
// SESSIONS_PORT is not repeated: the printed command, run in the same
// environment, already reaches it.
func commandScope(arguments []string) string {
	var words []string
	for index := 0; index < len(arguments); {
		switch arguments[index] {
		case "--json":
			index++
		case "--direct":
			words = append(words, "--direct")
			index++
		case "--host", "--port", "--machine":
			if index+1 >= len(arguments) || arguments[index+1] == "--" {
				return strings.Join(words, " ")
			}
			words = append(words, arguments[index], shellWord(arguments[index+1]))
			index += 2
		default:
			return strings.Join(words, " ")
		}
	}
	return strings.Join(words, " ")
}

var plainShellWord = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellWord quotes value for a POSIX shell unless it is already one plain word.
func shellWord(value string) string {
	if plainShellWord.MatchString(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// scopedCommand returns a daemon-written `sessions ...` command for the target
// this invocation reached. Anything else, and every command on the default
// target, is returned byte for byte.
func (a *app) scopedCommand(command string) string {
	if rest, ok := strings.CutPrefix(command, "sessions "); ok && a.scope != "" {
		return "sessions " + a.scope + " " + rest
	}
	return command
}

// scopedText scopes every backquoted `sessions ...` command inside a
// daemon-written sentence.
func (a *app) scopedText(text string) string {
	if a.scope == "" {
		return text
	}
	return strings.ReplaceAll(text, "`sessions ", "`sessions "+a.scope+" ")
}
