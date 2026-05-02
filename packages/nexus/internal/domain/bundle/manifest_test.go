package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

// ── WorkspaceIntentFromNexusfile ─────────────────────────────────────────────

func TestWorkspaceIntentFromNexusfile_Directory(t *testing.T) {
	dir := t.TempDir()
	writeNexusfile(t, dir, `
[dev]
init = ["npm install"]
up   = ["docker compose up -d"]
down = ["docker compose down"]
`)

	intent, err := WorkspaceIntentFromNexusfile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlice(t, "Init", intent.Init, []string{"npm install"})
	assertSlice(t, "Up", intent.Up, []string{"docker compose up -d"})
	assertSlice(t, "Down", intent.Down, []string{"docker compose down"})
}

func TestWorkspaceIntentFromNexusfile_FilePath(t *testing.T) {
	dir := t.TempDir()
	writeNexusfile(t, dir, `
[dev]
up = ["make up"]
`)
	// Pass the Nexusfile path directly instead of the directory.
	filePath := filepath.Join(dir, "Nexusfile")

	intent, err := WorkspaceIntentFromNexusfile(filePath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlice(t, "Up", intent.Up, []string{"make up"})
}

func TestWorkspaceIntentFromNexusfile_MissingFile(t *testing.T) {
	dir := t.TempDir() // no Nexusfile inside

	intent, err := WorkspaceIntentFromNexusfile(dir)
	if err != nil {
		t.Fatalf("missing Nexusfile must not error, got: %v", err)
	}
	if len(intent.Init)+len(intent.Up)+len(intent.Down)+len(intent.Bake) != 0 {
		t.Fatalf("expected empty intent for missing Nexusfile, got: %+v", intent)
	}
}

func TestWorkspaceIntentFromNexusfile_EmptyDevSection(t *testing.T) {
	dir := t.TempDir()
	writeNexusfile(t, dir, `
[vm]
profile = "default"
`)

	intent, err := WorkspaceIntentFromNexusfile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(intent.Up) != 0 || len(intent.Down) != 0 || len(intent.Init) != 0 {
		t.Fatalf("expected empty intent when [dev] is absent, got: %+v", intent)
	}
}

// ── WorkspaceIntent resources ────────────────────────────────────────────────

func TestWorkspaceIntentFromNexusfile_DevOnly(t *testing.T) {
	dir := t.TempDir()
	writeNexusfile(t, dir, `
[dev]
up = ["make up"]
`)

	intent, err := WorkspaceIntentFromNexusfile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlice(t, "Up", intent.Up, []string{"make up"})
	if intent.CPUs != 0 {
		t.Errorf("cpus: got %d, want 0", intent.CPUs)
	}
	if intent.MemMiB != 0 {
		t.Errorf("mem: got %d, want 0", intent.MemMiB)
	}
}

func TestWorkspaceIntentFromNexusfile_WithResources(t *testing.T) {
	dir := t.TempDir()
	writeNexusfile(t, dir, `
[vm]
cpus = 4
mem = 4096

[dev]
bake = ["apt-get update"]
up = ["./start.sh"]
`)

	intent, err := WorkspaceIntentFromNexusfile(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSlice(t, "Bake", intent.Bake, []string{"apt-get update"})
	assertSlice(t, "Up", intent.Up, []string{"./start.sh"})
	if intent.CPUs != 4 {
		t.Errorf("cpus: got %d, want 4", intent.CPUs)
	}
	if intent.MemMiB != 4096 {
		t.Errorf("mem: got %d, want 4096", intent.MemMiB)
	}
}

// ── MarshalManifest / ParseManifest roundtrip ────────────────────────────────

func TestMarshalParseManifest_Roundtrip(t *testing.T) {
	m := BundleManifest{
		SchemaVersion: SchemaVersion,
		BundleVersion: BundleVersion,
		CreatedAt:     "2026-01-01T00:00:00Z",
		Source: SourceMetadata{
			WorkspaceName: "test-ws",
			Ref:           "main",
			ProjectHint:   "/home/user/project",
		},
		Compatibility: CompatibilityMeta{
			Arch:     []string{"amd64"},
			Backend:  []string{"docker"},
			OsFamily: []string{"linux"},
		},
		WorkspaceIntent: WorkspaceIntent{
			Init: []string{"npm install"},
			Up:   []string{"docker compose up -d"},
			Down: []string{"docker compose down"},
		},
		Payload: PayloadIndex{Entries: []PayloadEntry{}},
		Integrity: IntegrityMetadata{
			ManifestDigest: "abc123",
			Algorithm:      "sha256",
		},
	}

	data, err := MarshalManifest(m)
	if err != nil {
		t.Fatalf("MarshalManifest: %v", err)
	}

	got, err := ParseManifest(data)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}

	if got.Source.WorkspaceName != m.Source.WorkspaceName {
		t.Errorf("WorkspaceName: got %q, want %q", got.Source.WorkspaceName, m.Source.WorkspaceName)
	}
	assertSlice(t, "Intent.Init", got.WorkspaceIntent.Init, m.WorkspaceIntent.Init)
	assertSlice(t, "Intent.Up", got.WorkspaceIntent.Up, m.WorkspaceIntent.Up)
	assertSlice(t, "Intent.Down", got.WorkspaceIntent.Down, m.WorkspaceIntent.Down)
	if got.Integrity.ManifestDigest != m.Integrity.ManifestDigest {
		t.Errorf("ManifestDigest: got %q, want %q", got.Integrity.ManifestDigest, m.Integrity.ManifestDigest)
	}
}

// ── ValidateManifest ─────────────────────────────────────────────────────────

func TestValidateManifest_Valid(t *testing.T) {
	m := minimalValidManifest()
	if err := ValidateManifest(m); err != nil {
		t.Fatalf("expected valid manifest to pass, got: %v", err)
	}
}

func TestValidateManifest_MissingSchemaVersion(t *testing.T) {
	m := minimalValidManifest()
	m.SchemaVersion = ""
	if err := ValidateManifest(m); err == nil {
		t.Fatal("expected error for missing schemaVersion")
	}
}

func TestValidateManifest_MissingWorkspaceName(t *testing.T) {
	m := minimalValidManifest()
	m.Source.WorkspaceName = ""
	if err := ValidateManifest(m); err == nil {
		t.Fatal("expected error for missing source.workspaceName")
	}
}

func TestValidateManifest_MissingDigest(t *testing.T) {
	m := minimalValidManifest()
	m.Integrity.ManifestDigest = ""
	if err := ValidateManifest(m); err == nil {
		t.Fatal("expected error for missing integrity.manifestDigest")
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func writeNexusfile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "Nexusfile"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertSlice(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: len=%d, want %d (%v)", label, len(got), len(want), want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d]: got %q, want %q", label, i, got[i], want[i])
		}
	}
}

func minimalValidManifest() BundleManifest {
	return BundleManifest{
		SchemaVersion: SchemaVersion,
		BundleVersion: BundleVersion,
		CreatedAt:     "2026-01-01T00:00:00Z",
		Source:        SourceMetadata{WorkspaceName: "ws"},
		Integrity:     IntegrityMetadata{ManifestDigest: "abc", Algorithm: "sha256"},
	}
}
