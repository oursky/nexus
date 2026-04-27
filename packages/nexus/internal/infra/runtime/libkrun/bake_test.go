//go:build linux

package libkrun

import (
	"testing"
	"time"
)

func TestBakeTimeout_Default(t *testing.T) {
	t.Setenv("NEXUS_LIBKRUN_BAKE_TIMEOUT", "")
	if got := bakeTimeout(); got != 12*time.Minute {
		t.Fatalf("expected default 12m0s, got %s", got)
	}
}

func TestBakeTimeout_Override(t *testing.T) {
	t.Setenv("NEXUS_LIBKRUN_BAKE_TIMEOUT", "45s")
	if got := bakeTimeout(); got != 45*time.Second {
		t.Fatalf("expected 45s, got %s", got)
	}
}

func TestBakeTimeout_InvalidFallsBack(t *testing.T) {
	t.Setenv("NEXUS_LIBKRUN_BAKE_TIMEOUT", "nope")
	if got := bakeTimeout(); got != 12*time.Minute {
		t.Fatalf("expected default 12m0s for invalid input, got %s", got)
	}
}

func TestBakeMaxAttempts_Default(t *testing.T) {
	t.Setenv("NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS", "")
	if got := bakeMaxAttempts(); got != 1 {
		t.Fatalf("expected default attempts 1, got %d", got)
	}
}

func TestBakeMaxAttempts_Override(t *testing.T) {
	t.Setenv("NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS", "2")
	if got := bakeMaxAttempts(); got != 2 {
		t.Fatalf("expected attempts 2, got %d", got)
	}
}

func TestBakeMaxAttempts_InvalidFallsBack(t *testing.T) {
	t.Setenv("NEXUS_LIBKRUN_BAKE_MAX_ATTEMPTS", "-1")
	if got := bakeMaxAttempts(); got != 1 {
		t.Fatalf("expected default attempts 1 for invalid input, got %d", got)
	}
}
