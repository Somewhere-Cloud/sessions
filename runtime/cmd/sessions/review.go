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

const reviewUsage = "usage: sessions review validate <snapshot.json>\n" +
	"       sessions review compare --snapshot FILE --questions FILE [--reviewer baseline | --reviewer exec:/ABS/PATH [--reviewer-arg ARG]... [--reviewer-env NAME]...]... [--timeout DURATION]"

// cmdReview is an explicitly invoked, experimental comparison. It reads only
// the files named on its command line and never contacts the daemon.
func (a *app) cmdReview(args []string) error {
	if len(args) == 0 {
		return fail(1, "%s", reviewUsage)
	}
	switch args[0] {
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
		path      string
		args, env []string
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
		case (flag == "--reviewer-arg" || flag == "--reviewer-env") && len(specs) > 0:
			last := specs[len(specs)-1]
			if flag == "--reviewer-arg" {
				last.args = append(last.args, value)
			} else {
				last.env = append(last.env, value)
			}
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
		external, err := review.NewExternal(spec.path, spec.args, spec.env, options.timeout)
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
