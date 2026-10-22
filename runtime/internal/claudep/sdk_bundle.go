package claudep

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// The release build generates the bundle before compiling the signed runner.
// Its bytes therefore participate in the existing binary signature/manifest.
//
//go:embed sdk-runtime/*
var sdkAssets embed.FS

func BundledSDKBridge(root string) (string, error) {
	data, err := sdkAssets.ReadFile("sdk-runtime/bridge.mjs")
	if err != nil {
		return "", fmt.Errorf("SDK bridge missing from this build; build the pinned SDK bundle before compiling the runner: %w", err)
	}
	digest := sha256.Sum256(data)
	dir := filepath.Join(root, "claude-sdk", fmt.Sprintf("%x", digest[:]))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "bridge.mjs")
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return path, nil
	}
	file, err := os.CreateTemp(dir, ".bridge-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return "", err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return "", writeErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
