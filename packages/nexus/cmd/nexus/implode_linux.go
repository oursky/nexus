//go:build linux

package main

import (
	_ "embed"
	"fmt"
	"io"
	"os/exec"
)

//go:embed scripts/firecracker-implode.sh
var firecrackerImplodeScript []byte

// buildImplodeScript prepends variable-export lines to the embedded implode
// script.  installBinDir is the user-local bin directory (e.g.
// /home/user/.local/bin).
func buildImplodeScript(installBinDir string) string {
	header := fmt.Sprintf(
		"export NEXUS_INSTALL_BIN_DIR=%s\n\n",
		shellQuote(installBinDir),
	)
	return header + string(firecrackerImplodeScript)
}

// killLibkrunOrphans kills any lingering passt or nexus libkrun-vm child
// processes left over from a previous daemon run. These are user-space
// processes (no root required) that must be stopped before the state
// directories are wiped.
func killLibkrunOrphans(w io.Writer) {
	for _, name := range []string{"passt", "nexus"} {
		// pkill matches by process name prefix; ignore errors (no processes = fine).
		_ = exec.Command("pkill", "-u", getUID(), "-f", name+".*libkrun").Run()
	}
	// Also kill any nexus libkrun-vm sub-processes spawned as children of the daemon.
	_ = exec.Command("pkill", "-u", getUID(), "-f", "libkrun-vm").Run()
	fmt.Fprintln(w, "  killed libkrun/passt orphan processes (if any)")
}

func getUID() string {
	out, _ := exec.Command("id", "-u").Output()
	uid := string(out)
	for len(uid) > 0 && (uid[len(uid)-1] == '\n' || uid[len(uid)-1] == '\r') {
		uid = uid[:len(uid)-1]
	}
	return uid
}

// runImplodePrivileged runs the privileged teardown script using the same
// privilege-escalation logic as the setup script.
func runImplodePrivileged(w io.Writer) error {
	installBinDir, err := resolveInstallBinDir()
	if err != nil {
		return fmt.Errorf("resolve install bin dir: %w", err)
	}

	script := buildImplodeScript(installBinDir)
	mode := resolvePrivilegeMode()
	if mode == privilegeModeManual {
		cmdPath := setupCommandPath()
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Run the following command to complete privileged cleanup:")
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "  sudo %s daemon implode\n", cmdPath)
		fmt.Fprintln(w, "")
		return fmt.Errorf("manual privileged step required — run the sudo command above")
	}
	fmt.Fprintln(w, "==> Running privileged cleanup script...")
	return setupRunScriptFn(mode, script)
}
