//go:build e2e && darwin

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func adhocSignNexusForHypervisor(binPath string) error {
	v := strings.TrimSpace(os.Getenv("NEXUS_E2E_SKIP_CODESIGN"))
	if v == "1" || strings.EqualFold(v, "true") {
		return nil
	}
	pl := filepath.Join(os.TempDir(), "nexus-e2e-hv-entitlements.plist")
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>com.apple.security.hypervisor</key>
    <true/>
    <key>com.apple.security.virtualization</key>
    <true/>
</dict>
</plist>
`
	if err := os.WriteFile(pl, []byte(plist), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(pl) }()
	cmd := exec.Command("codesign", "--entitlements", pl, "--force", "--sign", "-", binPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
