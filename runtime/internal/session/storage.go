package session

import (
	"context"
	"sort"

	"github.com/somewhere-tech/sessions/runtime/internal/storage"
)

// StorageInputs reads provenance without Git inspection, recovery or mutation.
// Closed and archived ledger paths still seed repository discovery.
func (m *Manager) StorageInputs(ctx context.Context) ([]storage.Seed, []storage.Reference, error) {
	states, err := m.ledgerStates(ctx)
	if err != nil {
		return nil, nil, err
	}
	seeds := []storage.Seed{}
	references := []storage.Reference{}
	bySession := map[string]storage.Reference{}
	seen := map[string]bool{}
	for _, lane := range states {
		if lane.WorktreePath != "" && !lane.WorktreeCleaned {
			seeds = append(seeds, storage.Seed{Path: lane.WorktreePath, Owned: true, Session: lane.LaneID})
			seen[lane.WorktreePath] = true
		}
	}
	for _, lane := range states {
		if !lane.RunnerExited && !lane.Reaped {
			bySession[lane.LaneID] = storage.Reference{Path: lane.Cwd, Session: lane.LaneID}
		}
		for _, path := range []string{lane.SourceRepo, lane.Cwd} {
			if path != "" && !seen[path] {
				seeds = append(seeds, storage.Seed{Path: path})
				seen[path] = true
			}
		}
	}
	for _, info := range m.registry.List(true) {
		if !seen[info.Cwd] {
			seeds = append(seeds, storage.Seed{Path: info.Cwd})
			seen[info.Cwd] = true
		}
		if !info.Exited {
			bySession[info.ID] = storage.Reference{Path: info.Cwd, Session: info.ID, PID: info.PID}
		}
	}
	for _, reference := range bySession {
		references = append(references, reference)
	}
	sort.Slice(references, func(i, j int) bool { return references[i].Session < references[j].Session })
	return seeds, references, nil
}
