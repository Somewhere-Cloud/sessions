package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// setAutoTitle replaces the in-memory automatic title status.
func (s *Session) setAutoTitle(status *AutoTitleStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == nil {
		s.info.AutoTitle = nil
		return
	}
	copied := *status
	s.info.AutoTitle = &copied
}

// SetAutoTitleStatus records the latest automatic title attempt on a live
// session. A session that is no longer live has nothing to show it on, so the
// call reports false and changes nothing.
func (r *Registry) SetAutoTitleStatus(id string, status *AutoTitleStatus) bool {
	session, live := r.Get(id)
	if !live {
		return false
	}
	session.setAutoTitle(status)
	return true
}

// generatedTitleAllowed is the ownership rule for a generated title. A
// person's rename and the provider's own title both outrank it, and a name
// someone supplied at launch is theirs unless the caller is regenerating on
// request after `sessions rename --auto` released it.
func generatedTitleAllowed(metadata *Metadata, replaceName bool) bool {
	switch metadata.NameSource {
	case NameSourceExplicit, NameSourceProvider:
		return false
	case NameSourceGenerated:
		return true
	}
	return replaceName || metadata.Name == ""
}

// AdoptGeneratedTitle stores a title Sessions generated. It never replaces a
// name a person or agent chose: the ownership check runs against the on-disk
// document, which survives restarts and is the record a concurrent rename
// writes first. It reports whether the name changed.
func (r *Registry) AdoptGeneratedTitle(id, title string, replaceName bool) (bool, error) {
	if !validMetadataID(id) {
		return false, fmt.Errorf("%w: session %s", ErrSessionNotFound, id)
	}
	name, err := validateSessionName(CompactConversationTitle(title))
	if err != nil {
		return false, err
	}
	session, live := r.Get(id)
	path := filepath.Join(r.config.RunnerStateDir, id+".json")
	changed := false
	_, err = updateMetadata(path, func(metadata *Metadata) error {
		if !generatedTitleAllowed(metadata, replaceName) {
			return nil
		}
		changed = metadata.Name != name || metadata.NameSource != NameSourceGenerated
		metadata.Name = name
		metadata.NameSource = NameSourceGenerated
		return nil
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("%w: session %s", ErrSessionNotFound, id)
		}
		return false, fmt.Errorf("persist generated session title: %w", err)
	}
	if changed && live {
		session.setName(name, NameSourceGenerated)
	}
	return changed, nil
}
