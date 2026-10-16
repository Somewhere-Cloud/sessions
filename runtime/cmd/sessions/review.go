package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/somewhere-tech/sessions/runtime/internal/review"
)

const reviewUsage = "usage: sessions review snapshot --observations FILE [--subject ID] [--task-file FILE] [--tail-file FILE]\n" +
	"       sessions review validate <snapshot.json>\n" +
	"       sessions review compare --snapshot FILE --questions FILE [--reviewer baseline | --reviewer exec:/ABS/PATH [--reviewer-arg ARG]...]... [--timeout DURATION]"

// cmdReview is an explicitly invoked, experimental comparison. It reads only
// the files named on its command line and never contacts the daemon.
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

// cmdReviewSnapshot exports one saved observation record (the output of
// `sessions --json status <session>`) into a frozen snapshot on stdout. Task
// text and a transcript tail are included only from files named here.
func (a *app) cmdReviewSnapshot(args []string) error {
	options := map[string]string{}
	for index := 0; index < len(args); index += 2 {
		flag := args[index]
		if index+1 >= len(args) || (flag != "--observations" && flag != "--subject" && flag != "--task-file" && flag != "--tail-file") {
			return fail(1, "%s", reviewUsage)
		}
		options[flag] = args[index+1]
	}
	if options["--observations"] == "" {
		return fail(1, "--observations is required; %s", reviewUsage)
	}
	record, err := readBounded(options["--observations"], review.MaxSnapshotBytes)
	if err != nil {
		return fail(1, "%s", err)
	}
	export := review.ExportOptions{Subject: options["--subject"], CapturedAt: time.Now(), Producer: "sessions review snapshot"}
	if path := options["--task-file"]; path != "" {
		text, err := readBounded(path, review.MaxTaskBytes)
		if err != nil {
			return fail(1, "%s", err)
		}
		export.Task = &review.Task{Text: string(text), Provenance: "file supplied by the caller: " + path}
	}
	if path := options["--tail-file"]; path != "" {
		text, err := readBounded(path, 4*review.MaxSnapshotBytes)
		if err != nil {
			return fail(1, "%s", err)
		}
		export.Tail = review.BoundedTail(string(text), "file supplied by the caller: "+path)
	}
	snapshot, err := review.ExportObservations(record, export)
	if err != nil {
		return fail(1, "%s", err)
	}
	_, err = fmt.Fprintf(a.stdout, "%s\n", snapshot)
	return err
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
	unknown := snapshot.UnknownFacts()
	if unknown == nil {
		unknown = []string{}
	}
	summary := map[string]any{"schema": review.SnapshotSchema, "subject": snapshot.Snapshot.Subject,
		"snapshot_hash": snapshot.Hash, "facts": len(snapshot.Snapshot.Facts), "unknown_facts": unknown,
		"task": snapshot.Snapshot.Task != nil, "tail": snapshot.Snapshot.Tail != nil}
	if a.wantJSON {
		return writeJSON(a.stdout, summary, true)
	}
	_, err = fmt.Fprintf(a.stdout, "valid %s snapshot %s\n  subject %s, %d facts, %d unknown\n", review.SnapshotSchema,
		snapshot.Hash, terminalSafe(snapshot.Snapshot.Subject), len(snapshot.Snapshot.Facts), len(unknown))
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	comparison := review.Compare(ctx, snapshot, questions, options.reviewers)
	if a.wantJSON {
		return writeJSON(a.stdout, comparison, true)
	}
	return writeReviewComparison(a.stdout, comparison)
}

func writeReviewComparison(out io.Writer, c review.Comparison) error {
	if _, err := fmt.Fprintf(out, "snapshot %s  questions %s\nunknown facts: %s\n", c.SnapshotHash, c.QuestionHash,
		terminalSafe(strings.Join(c.UnknownFacts, ", "))); err != nil {
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
