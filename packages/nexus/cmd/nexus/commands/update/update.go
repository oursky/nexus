package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/oursky/nexus/packages/nexus/internal/buildinfo"
	"github.com/spf13/cobra"
)

var (
	updateCheckOnce   sync.Once
	updateCheckResult bool
	updateCheckLatest string
)

// IsUpdateAvailable returns whether a newer release is available.
// Results are cached (sync.Once) for the lifetime of the process.
func IsUpdateAvailable(repo string) (bool, string) {
	updateCheckOnce.Do(func() {
		updateCheckResult, updateCheckLatest, _ = CheckForUpdate(repo)
	})
	return updateCheckResult, updateCheckLatest
}

func runUpdate(cmd *cobra.Command, checkOnly, noRestart bool, release, repo string) error {
	stderr := cmd.ErrOrStderr()
	stdout := cmd.OutOrStdout()

	// Resolve target release
	if release == "" {
		var err error
		release, err = resolveLatestTag(repo)
		if err != nil {
			return fmt.Errorf("update: resolve latest release: %w", err)
		}
	}
	if release == "" {
		return fmt.Errorf("update: could not determine latest release")
	}

	// Check if already up to date
	current := buildinfo.Version
	if current != "dev" && current == release {
		fmt.Fprintf(stdout, "already up to date (%s)\n", current)
		return nil
	}

	// Check-only mode
	if checkOnly {
		fmt.Fprintf(stdout, "update available: %s (current: %s)\n", release, current)
		return nil
	}

	// Resolve binary path
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("update: resolve current binary: %w", err)
	}

	// Construct URLs
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	binaryAsset := fmt.Sprintf("nexus-%s-%s", goos, goarch)
	downloadURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, release, binaryAsset)
	checksumsURL := fmt.Sprintf("https://github.com/%s/releases/download/%s/checksums.txt", repo, release)

	fmt.Fprintf(stderr, "update: downloading %s ...\n", binaryAsset)
	if err := downloadAndVerify(binaryPath, downloadURL, checksumsURL, binaryAsset); err != nil {
		return fmt.Errorf("update: %w", err)
	}

	fmt.Fprintf(stdout, "updated to %s\n", release)

	// Restart daemon
	if !noRestart {
		fmt.Fprintf(stderr, "update: restarting daemon ...\n")
		if err := restartDaemon(); err != nil {
			fmt.Fprintf(stderr, "update: daemon restart failed (non-fatal): %v\n", err)
			fmt.Fprintf(stderr, "update: run 'nexus daemon restart' manually\n")
		}
	}

	return nil
}

// CheckForUpdate checks GitHub for a newer release.
func CheckForUpdate(repo string) (bool, string, error) {
	current := buildinfo.Version
	if current == "dev" {
		return false, "", nil
	}

	latest, err := resolveLatestTag(repo)
	if err != nil {
		return false, "", err
	}

	if compareVersions(current, latest) < 0 {
		return true, latest, nil
	}
	return false, latest, nil
}

// resolveLatestTag fetches the latest release tag from the GitHub API.
func resolveLatestTag(repo string) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("parse GitHub release: %w", err)
	}

	return release.TagName, nil
}

// downloadAndVerify downloads the binary from url, verifies its SHA256
// against the checksums, and atomically replaces binaryPath.
func downloadAndVerify(binaryPath, url, checksumsURL, assetName string) error {
	tmpDir, err := os.MkdirTemp("", "nexus-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Download the binary
	downloadPath := filepath.Join(tmpDir, assetName)
	if err := downloadFile(url, downloadPath); err != nil {
		return fmt.Errorf("download: %w", err)
	}

	// Download checksums
	checksumsPath := filepath.Join(tmpDir, "checksums.txt")
	if err := downloadFile(checksumsURL, checksumsPath); err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}

	// Verify checksum
	checksumsData, err := os.ReadFile(checksumsPath)
	if err != nil {
		return fmt.Errorf("read checksums: %w", err)
	}

	// Compute SHA256 of downloaded binary
	hash := sha256.New()
	f, err := os.Open(downloadPath)
	if err != nil {
		return err
	}
	if _, err := io.Copy(hash, f); err != nil {
		f.Close()
		return err
	}
	f.Close()
	actualSum := hex.EncodeToString(hash.Sum(nil))

	// Find the checksum line for this asset
	found := false
	for _, line := range strings.Split(string(checksumsData), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == assetName {
			expectedSum := fields[0]
			if strings.EqualFold(actualSum, expectedSum) {
				found = true
			} else {
				return fmt.Errorf("checksum mismatch: got %s, want %s", actualSum, expectedSum)
			}
			break
		}
	}
	if !found {
		return fmt.Errorf("asset %s not found in checksums.txt", assetName)
	}

	// Atomic replace: write to .new, chmod, rename
	newPath := binaryPath + ".new"
	if err := os.Rename(downloadPath, newPath); err != nil {
		// Fallback: copy
		src, err := os.Open(downloadPath)
		if err != nil {
			return err
		}
		dst, err := os.Create(newPath)
		if err != nil {
			src.Close()
			return err
		}
		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			return err
		}
	}

	if err := os.Chmod(newPath, 0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", newPath, err)
	}

	if err := os.Rename(newPath, binaryPath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}

	return nil
}

// downloadFile downloads a URL to a local file path.
func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// compareVersions compares two version strings (strips "v" prefix).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareVersions(a, b string) int {
	a = strings.TrimPrefix(a, "v")
	b = strings.TrimPrefix(b, "v")

	if a == "dev" {
		return -1
	}
	if b == "dev" {
		return 1
	}

	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")

	n := len(partsA)
	if len(partsB) > n {
		n = len(partsB)
	}

	for i := 0; i < n; i++ {
		var va, vb int
		if i < len(partsA) {
			fmt.Sscanf(partsA[i], "%d", &va)
		}
		if i < len(partsB) {
			fmt.Sscanf(partsB[i], "%d", &vb)
		}
		if va < vb {
			return -1
		}
		if va > vb {
			return 1
		}
	}
	return 0
}

// restartDaemon stops and starts the daemon.
func restartDaemon() error {
	// Stop
	stopCmd := exec.Command("nexus", "daemon", "stop")
	stopCmd.Stdout = os.Stderr
	stopCmd.Stderr = os.Stderr
	_ = stopCmd.Run() // Best-effort; daemon may not be running

	// Start
	startCmd := exec.Command("nexus", "daemon", "start")
	startCmd.Stdout = os.Stderr
	startCmd.Stderr = os.Stderr
	return startCmd.Run()
}
