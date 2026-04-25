package project

import (
	"testing"
	"time"
)

func TestProjectFields(t *testing.T) {
	now := time.Now()
	p := Project{
		ID:        "proj-1",
		Name:      "My Project",
		RepoURL:   "https://github.com/example/repo",
		Config:    ProjectConfig{DefaultBackend: "libkrun", DefaultRef: "main"},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if p.ID != "proj-1" {
		t.Errorf("unexpected ID: %q", p.ID)
	}
	if p.Config.DefaultRef != "main" {
		t.Errorf("unexpected DefaultRef: %q", p.Config.DefaultRef)
	}
}

func TestProjectZeroValue(t *testing.T) {
	var p Project
	if p.ID != "" || p.Name != "" || p.RepoURL != "" {
		t.Error("zero-value Project should have empty string fields")
	}
	if p.Config.DefaultBackend != "" || p.Config.DefaultRef != "" {
		t.Error("zero-value ProjectConfig should have empty string fields")
	}
}

func TestErrorSentinelsDistinct(t *testing.T) {
	if ErrNotFound == ErrAlreadyExists {
		t.Error("ErrNotFound and ErrAlreadyExists must be distinct")
	}
}
