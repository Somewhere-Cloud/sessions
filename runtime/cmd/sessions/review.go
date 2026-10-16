package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/somewhere-tech/sessions/runtime/internal/review"
)

const reviewUsage = "usage: sessions review snapshot (<session> | --observations FILE) [--task-file FILE] [--tail-file FILE]\n" +
	"       sessions review validate <snapshot.json>\n" +
	"       sessions review compare --snapshot FILE --questions FILE [--reviewer baseline | --reviewer exec:/ABS/PATH [--reviewer-arg ARG]...]... [--timeout DURATION]"

// cmdReview is explicitly invoked and experimental. Snapshot may read a
// session's observations from the daemon; validate and compare read files only.
func (a *app) cmdReview(args []string) error {
	if len(args) == 0 {
		return fail(1, "%s", reviewUsage)
	}
	switch args[0] {
	case "snapshot":
		return a.cmdReviewSnapshot(args[1:])
	case "validate":
		return a.cmdReviewValidate(args[1:])
	case "compare":
		return a.cmdReviewCompare(args[1:])
	}
	return fail(1, "%s", reviewUsage)
}

func readBounded(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return data, nil
}

// cmdReviewSnapshot freezes one session's core observations into a snapshot
// on stdout. With <session> it reads the same record `sessions --json status`
// prints; with --observations it reads a saved copy of that record. Task text
// and a transcript tail are included only from files named here.
func (a *app) cmdReviewSnapshot(args []string) error {
	session, options, err := parseReviewSnapshot(args)
	if err != nil {
		return fail(1, "%s", err)
	}
	var record []byte
	if session != "" {
		status, err := a.resolveStatusRecord(session)
		if err != nil {
			return err
		}
		record = status.raw
	} else if record, err = readBounded(options["--observations"], review.MaxSnapshotBytes); err != nil {
		return fail(1, "%s", err)
	}
	export := review.ExportOptions{CapturedAt: time.Now(), Producer: "sessions review snapshot"}
	if path := options["--task-file"]; path != "" {
		text, err := readBounded(path, review.MaxTaskBytes)
		if err == nil && !utf8.Valid(text) {
			err = fmt.Errorf("--task-file %s is not valid UTF-8; convert it to UTF-8 and export again", path)
		}
		if err != nil {
			return fail(1, "%s", err)
		}
		export.Task = &review.Task{Text: string(text), Provenance: "file supplied by the caller: " + path}
	}
	if path := options["--tail-file"]; path != "" {
		if export.Tail, err = readTailFile(path); err != nil {
			return fail(1, "%s", err)
		}
	}
	snapshot, err := review.ExportObservations(record, export)
	if err != nil {
		return fail(1, "%s", err)
	}
	_, err = fmt.Fprintf(a.stdout, "%s\n", snapshot)
	return err
}

// readTailFile reads only the end of a regular file: the last
// review.MaxTailBytes, plus a few bytes to land on a UTF-8 boundary. A long
// log is therefore bounded rather than refused, and omitted_bytes counts
// everything before the kept text. Only regular files are read, so a FIFO or
// device cannot block or stream without end.
func readTailFile(path string) (*review.Tail, error) {
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("--tail-file %s must be a readable regular file; save the excerpt to a file first", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("--tail-file %s must be a readable regular file; save the excerpt to a file first", path)
	}
	offset := max(info.Size()-int64(review.MaxTailBytes+utf8.UTFMax), 0)
	text, err := io.ReadAll(io.NewSectionReader(file, offset, info.Size()-offset))
	if err != nil {
		return nil, err
	}
	tail := review.BoundedTail(string(text), "file supplied by the caller: "+path)
	if !utf8.ValidString(tail.Text) {
		return nil, fmt.Errorf("--tail-file %s is not valid UTF-8; convert it to UTF-8 and export again", path)
	}
	tail.OmittedBytes += int(offset)
	return tail, nil
}

func parseReviewSnapshot(args []string) (string, map[string]string, error) {
	options := map[string]string{}
	session := ""
	for index := 0; index < len(args); index++ {
		flag := args[index]
		switch {
		case flag == "--observations" || flag == "--task-file" || flag == "--tail-file":
			if index+1 >= len(args) || args[index+1] == "" {
				return "", nil, fmt.Errorf("%s needs a file; %s", flag, reviewUsage)
			}
			options[flag] = args[index+1]
			index++
		case !strings.HasPrefix(flag, "-") && session == "":
			session = flag
		default:
			return "", nil, fmt.Errorf("unexpected %s; %s", flag, reviewUsage)
		}
	}
	if (session == "") == (options["--observations"] == "") {
		return "", nil, fmt.Errorf("name exactly one of a session or --observations FILE; %s", reviewUsage)
	}
	return session, options, nil
}

func (a *app) cmdReviewValidate(args []string) error {
	if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
		return fail(1, "%s", reviewUsage)
	}
	data, err := readBounded(args[0], review.MaxSnapshotBytes)
	if err != nil {
		return fail(1, "%s", err)
	}
	snapshot, err := review.DecodeSnapshot(data)
	if err != nil {
		return fail(1, "%s", err)
	}
	unknown := snapshot.NullPaths()
	summary := map[string]any{"schema": review.SnapshotSchema, "subject": snapshot.Snapshot.Subject,
		"snapshot_hash": snapshot.Hash, "observations_version": review.ObservationsVersion, "null_paths": unknown,
		"task": snapshot.Snapshot.Task != nil, "tail": snapshot.Snapshot.Tail != nil}
	if a.wantJSON {
		return writeJSON(a.stdout, summary, true)
	}
	_, err = fmt.Fprintf(a.stdout, "valid %s snapshot %s\n  subject %s, observations version %d, %d null (unknown) values\n",
		review.SnapshotSchema, snapshot.Hash, terminalSafe(snapshot.Snapshot.Subject), review.ObservationsVersion, len(unknown))
	return err
}

type reviewCompareOptions struct {
	snapshot, questions string
	timeout             time.Duration
	reviewers           []review.Reviewer
}

func parseReviewCompare(args []string) (reviewCompareOptions, error) {
	options := reviewCompareOptions{timeout: review.DefaultTimeout}
	type pending struct {
		path string
		args []string
	}
	var specs []*pending
	baseline := false
	for index := 0; index < len(args); index++ {
		flag := args[index]
		if index+1 >= len(args) {
			return options, fmt.Errorf("%s needs a value; %s", flag, reviewUsage)
		}
		value := args[index+1]
		index++
		switch {
		case flag == "--snapshot":
			options.snapshot = value
		case flag == "--questions":
			options.questions = value
		case flag == "--timeout":
			parsed, err := time.ParseDuration(value)
			if err != nil || parsed <= 0 {
				return options, fmt.Errorf("--timeout needs a positive duration such as 30s")
			}
			options.timeout = parsed
		case flag == "--reviewer" && value == "baseline":
			baseline = true
		case flag == "--reviewer" && strings.HasPrefix(value, "exec:"):
			specs = append(specs, &pending{path: strings.TrimPrefix(value, "exec:")})
		case flag == "--reviewer-arg" && len(specs) > 0:
			specs[len(specs)-1].args = append(specs[len(specs)-1].args, value)
		default:
			return options, fmt.Errorf("unexpected %s %s; %s", flag, value, reviewUsage)
		}
	}
	if options.snapshot == "" || options.questions == "" {
		return options, fmt.Errorf("--snapshot and --questions are required; %s", reviewUsage)
	}
	if baseline || len(specs) == 0 {
		options.reviewers = append(options.reviewers, review.Baseline{})
	}
	for _, spec := range specs {
		external, err := review.NewExternal(spec.path, spec.args, options.timeout)
		if err != nil {
			return options, err
		}
		options.reviewers = append(options.reviewers, external)
	}
	return options, nil
}

func (a *app) cmdReviewCompare(args []string) error {
	options, err := parseReviewCompare(args)
	if err != nil {
		return fail(1, "%s", err)
	}
	snapshotData, err := readBounded(options.snapshot, review.MaxSnapshotBytes)
	if err != nil {
		return fail(1, "%s", err)
	}
	snapshot, err := review.DecodeSnapshot(snapshotData)
	if err != nil {
		return fail(1, "%s", err)
	}
	questionData, err := readBounded(options.questions, review.MaxSnapshotBytes)
	if err != nil {
		return fail(1, "%s", err)
	}
	questions, err := review.DecodeQuestions(questionData)
	if err != nil {
		return fail(1, "%s", err)
	}
	ctx, stop := reviewContext()
	defer stop()
	comparison := review.Compare(ctx, snapshot, questions, options.reviewers)
	if a.wantJSON {
		return writeJSON(a.stdout, comparison, true)
	}
	return writeReviewComparison(a.stdout, comparison)
}

// reviewContext is cancelled by any of reviewCancelSignals, so the
// comparison stops its adapter rather than the CLI exiting and leaving the
// adapter's process group running.
func reviewContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), reviewCancelSignals...)
}

func writeReviewComparison(out io.Writer, c review.Comparison) error {
	if _, err := fmt.Fprintf(out, "snapshot %s  questions %s\nnull (unknown) observation values: %d\n", c.SnapshotHash,
		c.QuestionHash, len(c.NullPaths)); err != nil {
		return err
	}
	for _, result := range c.Results {
		detail := ""
		switch {
		case result.Error != nil:
			detail = string(result.Error.Kind) + ": " + result.Error.Detail
		case result.Answer != nil:
			detail = result.Answer.Choice
		}
		if _, err := fmt.Fprintf(out, "  %-40s %-9s %s\n", terminalSafe(result.Reviewer.ID), result.Status, terminalSafe(detail)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, c.Note)
	return err
}
