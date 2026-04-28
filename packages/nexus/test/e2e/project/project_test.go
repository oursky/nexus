//go:build e2e

package project_test

import (
	"testing"

	"github.com/oursky/nexus/packages/nexus/test/e2e/harness"
)

// Spec: PRJ-010, PRJ-011, PRJ-012, PRJ-013, PRJ-014, PRJ-015, PRJ-016, PRJ-017, PRJ-018, PRJ-019, PRJ-020, PRJ-030, INV-003, INV-004, INV-015
func TestProject(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	repo := harness.MakeLocalGitRepo(t, "project")

	// 1. Create a project
	var createRes struct {
		Project struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			RepoURL string `json:"repoUrl"`
		} `json:"project"`
	}
	h.MustCall("project.create", map[string]any{
		"name":    "e2e-test-project",
		"repoUrl": repo,
	}, &createRes)
	id := createRes.Project.ID
	if id == "" {
		t.Fatal("create: got empty project id")
	}
	if createRes.Project.Name != "e2e-test-project" {
		t.Fatalf("create: expected name %q, got %q", "e2e-test-project", createRes.Project.Name)
	}
	if createRes.Project.RepoURL != repo {
		t.Fatalf("create: expected repoUrl %q, got %q", repo, createRes.Project.RepoURL)
	}

	// 2. List and verify the new project is present
	var listRes struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	h.MustCall("project.list", nil, &listRes)
	found := false
	for _, p := range listRes.Projects {
		if p.ID == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("list: project %s not found after create", id)
	}

	// 3. Get by ID and verify fields
	var getRes struct {
		Project struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			RepoURL string `json:"repoUrl"`
		} `json:"project"`
	}
	h.MustCall("project.get", map[string]any{"id": id}, &getRes)
	if getRes.Project.ID != id {
		t.Fatalf("get: expected id %q, got %q", id, getRes.Project.ID)
	}
	if getRes.Project.Name != "e2e-test-project" {
		t.Fatalf("get: expected name %q, got %q", "e2e-test-project", getRes.Project.Name)
	}
	if getRes.Project.RepoURL != repo {
		t.Fatalf("get: expected repoUrl %q, got %q", repo, getRes.Project.RepoURL)
	}

	// 4. Delete the project
	var removeRes struct {
		Removed bool `json:"removed"`
	}
	h.MustCall("project.remove", map[string]any{"id": id}, &removeRes)
	if !removeRes.Removed {
		t.Fatal("remove: expected removed=true")
	}

	// 5. List again and verify it's gone
	h.MustCall("project.list", nil, &listRes)
	for _, p := range listRes.Projects {
		if p.ID == id {
			t.Fatalf("list: project %s still present after remove", id)
		}
	}
}

// Spec: ERR-040, INV-004
// TestProject_DuplicateName verifies project.create rejects duplicate names.
func TestProject_DuplicateName(t *testing.T) {
	t.Parallel()
	h := harness.New(t)
	repo := harness.MakeLocalGitRepo(t, "proj-dup")

	var res struct {
		Project struct{ ID string `json:"id"` } `json:"project"`
	}
	h.MustCall("project.create", map[string]any{
		"name":    "dup-proj-name",
		"repoUrl": repo,
	}, &res)
	id := res.Project.ID
	t.Cleanup(func() { _ = h.Call("project.remove", map[string]any{"id": id}, nil) })

	err := h.Call("project.create", map[string]any{
		"name":    "dup-proj-name",
		"repoUrl": repo,
	}, nil)
	if err == nil {
		t.Fatal("create duplicate project name: expected error, got nil")
	}
}
