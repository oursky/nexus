//go:build linux

package firecracker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const bridgeName = "nexusbr0"
const bridgeGatewayIP = "172.26.0.1"
const guestSubnetCIDR = "172.26.0.0/16"
const tapHelperBin = "nexus-tap-helper"

// resolveTapHelperPath returns the absolute path to nexus-tap-helper.
// Checks the user-local install location first (where nexus daemon setup puts
// it) so the daemon works even when ~/.local/bin is not in PATH.
func resolveTapHelperPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".local", "bin", "nexus-tap-helper")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(tapHelperBin); err == nil {
		return p
	}
	return tapHelperBin // unchanged — will fail naturally with a clear message
}

func checkTapHelper() error {
	path := resolveTapHelperPath()
	if _, err := os.Stat(path); err != nil && path == tapHelperBin {
		return fmt.Errorf(
			"%s not found (checked ~/.local/bin and PATH)\n\nOne-time setup required:\n%s",
			tapHelperBin, tapHelperSetupInstructions(),
		)
	}

	out, err := exec.Command("getcap", path).Output()
	if err != nil {
		return nil
	}
	if !strings.Contains(string(out), "cap_net_admin") {
		return fmt.Errorf(
			"%s at %s lacks cap_net_admin\n\nRun:\n  sudo setcap cap_net_admin=ep %s",
			tapHelperBin, path, path,
		)
	}
	return nil
}

func checkBridge() error {
	out, err := exec.Command("ip", "link", "show", bridgeName).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"bridge %s not found\n\nOne-time setup required:\n%s",
			bridgeName, bridgeSetupInstructions(),
		)
	}
	if !strings.Contains(string(out), "UP") {
		return fmt.Errorf(
			"bridge %s exists but is not UP\n\nTry: sudo ip link set %s up\nOr re-run full setup:\n%s",
			bridgeName, bridgeName, bridgeSetupInstructions(),
		)
	}
	return nil
}

func tapHelperSetupInstructions() string {
	return "  nexus daemon start   # auto-provisions on first run"
}

func bridgeSetupInstructions() string {
	return "  sudo tee /etc/systemd/network/10-nexusbr0.netdev << 'EOF'\n" +
		"[NetDev]\nName=nexusbr0\nKind=bridge\nEOF\n\n" +
		"  sudo tee /etc/systemd/network/11-nexusbr0.network << 'EOF'\n" +
		"[Match]\nName=nexusbr0\n[Network]\nAddress=172.26.0.1/16\nIPForward=yes\nIPMasquerade=ipv4\nEOF\n\n" +
		"  sudo tee /etc/systemd/network/12-nexus-tap.network << 'EOF'\n" +
		"[Match]\nName=nexus-*\n[Network]\nBridge=nexusbr0\nEOF\n\n" +
		"  sudo systemctl enable --now systemd-networkd"
}

func realSetupTAP(tapName, hostIP, subnetCIDR string) (any, error) {
	bin := resolveTapHelperPath()
	out, err := exec.Command(bin, "create", tapName, bridgeName).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// Existing TAPs can race with cleanup; treat EBUSY as idempotent success.
		if strings.Contains(msg, "device or resource busy") {
			return nil, nil
		}
		return nil, fmt.Errorf("nexus-tap-helper create %s: %w: %s", tapName, err, msg)
	}
	return nil, nil
}

func realTeardownTAP(tapName, subnetCIDR string) {
	_ = exec.Command(resolveTapHelperPath(), "delete", tapName).Run()
}
