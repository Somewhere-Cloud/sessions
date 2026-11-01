package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/somewhere-tech/sessions/runtime/internal/project"
)

func TestNewProjectAndScratchFoldersAreMadeInTheHomeProjectsFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	daemon := newTestDaemon(t)
	server := daemon.handler
	server.folders = project.DefaultFolders(home)
	server.projects.SetScratchRoot(server.folders.ScratchRoot)

	var made map[string]any
	response := serve(t, daemon.handler, http.MethodPost, "/api/projects/new", strings.NewReader(`{"name":"Weather Bot"}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &made) != nil {
		t.Fatalf("new project %d %s", response.Code, response.Body.String())
	}
	if made["path"] != filepath.Join(home, "Projects", "Weather-Bot") || made["name"] != "Weather Bot" || made["project_id"] == nil {
		t.Fatalf("new project = %v", made)
	}
	stored, err := server.projects.Resolve(filepath.Join(home, "Projects", "Weather-Bot"))
	if err != nil || stored.Name != "Weather Bot" || stored.Implicit {
		t.Fatalf("stored project = %+v, %v", stored, err)
	}

	response = serve(t, daemon.handler, http.MethodPost, "/api/projects/scratch", strings.NewReader(`{"hint":"try a parser"}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &made) != nil {
		t.Fatalf("scratch %d %s", response.Code, response.Body.String())
	}
	path, _ := made["path"].(string)
	if filepath.Dir(path) != filepath.Join(home, "Projects", "scratch") || !strings.Contains(filepath.Base(path), "-try-a-parser-") || made["project_id"] != project.ScratchProjectID {
		t.Fatalf("scratch = %v", made)
	}

	response = serve(t, daemon.handler, http.MethodPost, "/api/projects/new", strings.NewReader(`{"name":"???"}`), "127.0.0.1:4567", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("nameless project answered %d %s", response.Code, response.Body.String())
	}
}
