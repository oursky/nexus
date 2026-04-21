//go:build linux

package main

import (
	_ "embed"
	"fmt"
	"io"
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
