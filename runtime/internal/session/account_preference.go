package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/somewhere-tech/sessions/runtime/internal/state"
)

// A preference selects a provider home for new independent sessions only. It
// never rewrites provider credentials or changes an existing lane's account.
func (m *Manager) preferredAccount(tool string) (string, error) {
	if m.config.UserStateRoot == "" || (tool != "claude" && tool != "codex") {
		return "", nil
	}
	path := filepath.Join(m.config.UserStateRoot, "account-defaults", tool+".json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read default account: %w", err)
	}
	var value struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("read default account: %w; repair with sessions accounts default <name> --tool %s", err, tool)
	}
	if value.Name != "" {
		if err := state.ValidateProfileName(value.Name); err != nil {
			return "", err
		}
	}
	return value.Name, nil
}

func (m *Manager) checkPreferredAccount(tool, name string) error {
	if err := state.ValidateProfileName(name); err != nil {
		return err
	}
	info, err := os.Stat(m.AccountHomePath(tool, name))
	if err != nil || !info.IsDir() || readAccountSidecar(m.config.UserStateRoot, tool, name).Removed {
		return errors.New("the selected default account is no longer registered; choose a default in Accounts or pass an explicit account")
	}
	return nil
}

func (m *Manager) SetDefaultAccount(tool, name string) error {
	if tool != "claude" && tool != "codex" {
		return errors.New("choose claude or codex")
	}
	if m.config.UserStateRoot == "" {
		return errors.New("account preferences require a configured user state root")
	}
	m.accountMetadataMu.Lock()
	defer m.accountMetadataMu.Unlock()
	if name != "" {
		if err := m.checkPreferredAccount(tool, name); err != nil {
			return err
		}
	}
	root := filepath.Join(m.config.UserStateRoot, "account-defaults")
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		Name string `json:"name"`
	}{name})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(root, ".preference-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(root, tool+".json"))
}
