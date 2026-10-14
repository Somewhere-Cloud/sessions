package main

import (
	"fmt"
	"github.com/somewhere-tech/sessions/runtime/internal/storage"
)

func (a *app) cmdStorage(args []string) error {
	path := "/api/storage"
	if len(args) == 1 && args[0] == "preview" {
		path += "/preview"
	} else if len(args) != 0 {
		return fail(1, "usage: sessions storage [preview] [--json]")
	}
	var report storage.Report
	if err := a.getJSON(path, &report); err != nil {
		return err
	}
	if a.wantJSON {
		return writeJSON(a.stdout, report, true)
	}
	if _, err := fmt.Fprintf(a.stdout, "Storage on %s (%s) — read-only preview\nLogical bytes: %d; allocated bytes: %s; confidence: %s\n", report.MachineName, report.MachineID, report.Totals.Logical, storageAllocated(report.Totals), report.Totals.Confidence); err != nil {
		return err
	}
	for _, root := range report.Roots {
		if _, err := fmt.Fprintf(a.stdout, "\nkept %s [ownership %s]\n  %d logical bytes; %s allocated; %s\n  Git dirty=%s untracked=%s unpushed=%s; runner references=%d\n  Source and unknown data retained; worktree teardown is not implemented here.\n", root.Path, root.Ownership, root.Bytes.Logical, storageAllocated(root.Bytes), root.Bytes.Confidence, root.Git.Dirty, root.Git.Untracked, root.Git.Unpushed, len(root.References)); err != nil {
			return err
		}
		for _, candidate := range root.Candidates {
			if _, err := fmt.Fprintf(a.stdout, "  %s %s (%s; %d logical bytes; %s allocated; %s)\n", candidate.Action, candidate.Path, candidate.Category, candidate.Bytes.Logical, storageAllocated(candidate.Bytes), candidate.Bytes.Confidence); err != nil {
				return err
			}
			for _, reason := range candidate.Reasons {
				if _, err := fmt.Fprintln(a.stdout, "    "+reason); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(a.stdout, "    Restore suggestion: "+candidate.Restore); err != nil {
				return err
			}
		}
		for _, issue := range root.Issues {
			if _, err := fmt.Fprintln(a.stdout, "  "+issue); err != nil {
				return err
			}
		}
	}
	for _, message := range append(report.Issues, report.Limitations...) {
		if _, err := fmt.Fprintln(a.stdout, message); err != nil {
			return err
		}
	}
	return nil
}
func storageAllocated(value storage.Bytes) string {
	if value.Allocated == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d", *value.Allocated)
}
