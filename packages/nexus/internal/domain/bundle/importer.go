package bundle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"runtime"
)

// Importer validates and imports a .nxbundle archive.
type Importer struct{}

// NewImporter constructs an Importer.
func NewImporter() *Importer {
	return &Importer{}
}

// Import opens a .nxbundle, validates it, and (when dryRun is false) provisions the workspace.
func (imp *Importer) Import(ctx context.Context, bundlePath string, dryRun bool) error {
	// Open archive.
	f, err := os.Open(bundlePath)
	if err != nil {
		return fmt.Errorf("bundle: open bundle: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return &InvalidBundle{Reason: fmt.Sprintf("not a valid gzip archive: %v", err)}
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	// Scan archive: extract manifest bytes and check for workspace payload.
	manifestBytes, hasWorkspacePayload, err := scanBundle(tr)
	if err != nil {
		return err
	}

	// Parse manifest.
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return &InvalidBundle{Reason: fmt.Sprintf("cannot parse manifest.json: %v", err)}
	}

	// Validate required fields.
	if err := ValidateManifest(manifest); err != nil {
		return err
	}

	// Verify integrity: the exporter computes the sha256 digest over the canonical manifest
	// bytes with the integrity.digest field zeroed (empty string), then stores the digest.
	// We re-create the same canonical bytes (zero-out digest, re-serialise, hash) to validate.
	if err := verifyIntegrity(manifest, manifestBytes); err != nil {
		return err
	}

	// Check arch compatibility.
	if err := checkCompatibility(manifest); err != nil {
		return err
	}

	if dryRun {
		printCompatibilityReport(manifest, hasWorkspacePayload)
		return nil
	}

	// v1 stub: workspace provisioning not yet implemented.
	fmt.Printf("import complete: workspace %q (ref: %s) — provisioning stub, no workspace created\n",
		manifest.Source.WorkspaceName, manifest.Source.Ref)
	return nil
}

// scanBundle reads the tar archive and returns the bytes of manifest.json
// and whether payload/workspace.tar.gz is present.
func scanBundle(tr *tar.Reader) (manifestBytes []byte, hasWorkspacePayload bool, err error) {
	for {
		hdr, hdrErr := tr.Next()
		if hdrErr == io.EOF {
			break
		}
		if hdrErr != nil {
			return nil, false, &InvalidBundle{Reason: fmt.Sprintf("read archive: %v", hdrErr)}
		}
		switch hdr.Name {
		case "manifest.json":
			data, readErr := io.ReadAll(tr)
			if readErr != nil {
				return nil, false, &InvalidBundle{Reason: fmt.Sprintf("read manifest.json: %v", readErr)}
			}
			manifestBytes = data
		case "payload/workspace.tar.gz":
			hasWorkspacePayload = true
		}
	}
	if manifestBytes == nil {
		return nil, false, &InvalidBundle{Reason: "manifest.json not found in bundle"}
	}
	return manifestBytes, hasWorkspacePayload, nil
}

// extractManifest reads the tar archive and returns the bytes of manifest.json.
func extractManifest(tr *tar.Reader) ([]byte, error) {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, &InvalidBundle{Reason: fmt.Sprintf("read archive: %v", err)}
		}
		if hdr.Name == "manifest.json" {
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, &InvalidBundle{Reason: fmt.Sprintf("read manifest.json: %v", err)}
			}
			return data, nil
		}
	}
	return nil, &InvalidBundle{Reason: "manifest.json not found in bundle"}
}

// verifyIntegrity checks the stored manifestDigest by zeroing the integrity fields,
// re-serialising, and re-hashing — matching the exporter's canonical-bytes scheme.
func verifyIntegrity(manifest BundleManifest, _ []byte) error {
	if manifest.Integrity.Algorithm != "sha256" {
		return &InvalidBundle{Reason: fmt.Sprintf("unsupported digest algorithm: %s", manifest.Integrity.Algorithm)}
	}
	stored := manifest.Integrity.ManifestDigest

	// Zero out the digest to reproduce the canonical pre-digest bytes.
	canonical := manifest
	canonical.Integrity.ManifestDigest = ""
	canonicalBytes, err := MarshalManifest(canonical)
	if err != nil {
		return &InvalidBundle{Reason: fmt.Sprintf("re-serialise for integrity check: %v", err)}
	}

	h := sha256.New()
	h.Write(canonicalBytes)
	got := hex.EncodeToString(h.Sum(nil))
	if got != stored {
		return &IntegrityViolation{Expected: stored, Got: got}
	}
	return nil
}

// checkCompatibility returns IncompatibleHost if the current arch is not listed.
func checkCompatibility(manifest BundleManifest) error {
	arch := runtime.GOARCH
	for _, a := range manifest.Compatibility.Arch {
		if a == arch {
			return nil
		}
	}
	return &IncompatibleHost{Want: manifest.Compatibility.Arch, Got: arch}
}

// printCompatibilityReport prints a human-readable dry-run report to stdout.
func printCompatibilityReport(manifest BundleManifest, hasWorkspacePayload bool) {
	fmt.Printf("dry-run compatibility report\n")
	fmt.Printf("  workspace:  %s\n", manifest.Source.WorkspaceName)
	fmt.Printf("  ref:        %s\n", manifest.Source.Ref)
	fmt.Printf("  arch:       %v — compatible\n", manifest.Compatibility.Arch)
	fmt.Printf("  backend:    %v\n", manifest.Compatibility.Backend)
	fmt.Printf("  osFamily:   %v\n", manifest.Compatibility.OsFamily)
	digestPreview := manifest.Integrity.ManifestDigest
	if len(digestPreview) > 12 {
		digestPreview = digestPreview[:12]
	}
	fmt.Printf("  integrity:  %s (%s) OK\n", digestPreview, manifest.Integrity.Algorithm)
	if hasWorkspacePayload {
		fmt.Printf("  payload:    workspace.tar.gz present\n")
	} else {
		fmt.Printf("  payload:    stub (no workspace files)\n")
	}
	fmt.Printf("workspace intent (from Nexusfile):\n")
	fmt.Printf("  workspace.init: %v\n", manifest.WorkspaceIntent.Init)
	fmt.Printf("  workspace.up:   %v\n", manifest.WorkspaceIntent.Up)
	fmt.Printf("  workspace.down: %v\n", manifest.WorkspaceIntent.Down)
	fmt.Printf("  initMode:       %s\n", manifest.WorkspaceIntent.InitMode)
}
