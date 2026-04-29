//go:build linux

package libkrun

import (
	"context"
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// pickFreePort returns a free ephemeral TCP port on 127.0.0.1.
func pickFreePort() (int, error) {
	ln, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		return 0, fmt.Errorf("socket: %w", err)
	}
	defer syscall.Close(ln)

	addr := &syscall.SockaddrInet4{Port: 0, Addr: [4]byte{127, 0, 0, 1}}
	if err := syscall.Bind(ln, addr); err != nil {
		return 0, fmt.Errorf("bind: %w", err)
	}
	sa, err := syscall.Getsockname(ln)
	if err != nil {
		return 0, fmt.Errorf("getsockname: %w", err)
	}
	return sa.(*syscall.SockaddrInet4).Port, nil
}

func resolvePasstPath() string {
	home, _ := os.UserHomeDir()
	if home != "" {
		p := filepath.Join(home, ".local", "bin", "passt")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("passt"); err == nil {
		return p
	}
	return "passt"
}

func createPasstSocketPair() (vmSide *os.File, hostSide *os.File, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("socketpair: %w", err)
	}
	return os.NewFile(uintptr(fds[0]), "passt-vm"), os.NewFile(uintptr(fds[1]), "passt-host"), nil
}

// unshareUserNSAvailable caches whether "unshare -U true" works on this host.
// In containerized CI runners, unprivileged users lack CAP_SYS_ADMIN needed for
// passt's self-sandboxing. Running passt inside a user namespace gives it the
// capability to create isolating namespaces.
var unshareUserNSAvailable = sync.OnceValue(func() bool {
	if _, err := exec.LookPath("unshare"); err != nil {
		return false
	}
	cmd := exec.Command("unshare", "-U", "true")
	return cmd.Run() == nil
})

func startPasstProcess(ctx context.Context, workDir string, hostSide *os.File, sshHostPort int, guestIPv4 string) (*os.Process, error) {
	logPath := filepath.Join(workDir, "passt.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		logFile = os.Stderr
	}
	passtBin := resolvePasstPath()
	args := []string{
		"--fd", strconv.Itoa(3),
		"--foreground",
		"--stderr",
	}
	if guestIPv4 = strings.TrimSpace(guestIPv4); guestIPv4 != "" {
		// Keep passt's DHCP yiaddr aligned with the agent's static fallback so
		// host->guest forwarded TCP reaches the same IPv4 even when DHCPv4 setup
		// inside the guest fails and we fall back to a static address.
		args = append(args, "--address", guestIPv4+"/16")
	}
	// Forward host loopback port → guest port 22 so Remote-SSH (Mac → ProxyJump → VM) works.
	// The GuestIP stored in the workspace record uses this same port.
	if sshHostPort > 0 {
		args = append(args, "-t", fmt.Sprintf("127.0.0.1/%d:22", sshHostPort))
	}

	// In unprivileged containers, passt can't self-sandbox because it lacks
	// CAP_SYS_ADMIN for namespace operations. Wrap it in a user namespace so
	// it gains the capability while still operating on the host network stack.
	//
	// We use -r (--map-root-user) so passt sees UID 0 inside the namespace.
	// Without -r, passt still somehow hits its "drop to nobody" path and
	// fails on setgid(65534) because the GID isn't mapped. With -r, passt
	// sees root, skips the nobody drop (because ns_is_init() is false in a
	// user namespace), and stays as UID 0 / GID 0, which is always valid.
	cmdBin := passtBin
	cmdArgs := args
	if unshareUserNSAvailable() {
		cmdArgs = append([]string{"-Ur", "--", passtBin}, args...)
		cmdBin = "unshare"
	}

	cmd := exec.CommandContext(ctx, cmdBin, cmdArgs...)
	cmd.ExtraFiles = []*os.File{hostSide}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start passt: %w", err)
	}
	_ = logFile.Close()
	return cmd.Process, nil
}

func passtMACForWorkspaceID(workspaceID string) [6]byte {
	h := fnv.New32a()
	_, _ = h.Write([]byte(workspaceID))
	v := h.Sum32()
	return [6]byte{0x02, byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v), 0x01}
}

func passtGuestIPv4ForWorkspace(workspaceID string) string {
	gateway := strings.TrimSpace(hostDefaultGatewayIP())
	if gateway == "" {
		gateway = "192.168.0.1"
	}
	mac := passtMACForWorkspaceID(workspaceID)
	return staticGuestIPv4ForMAC(fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]), gateway)
}

func hostDNSServers() []string {
	sources := []string{
		"/etc/resolv.conf",
		"/run/systemd/resolve/resolv.conf",
	}
	seen := map[string]struct{}{}
	servers := make([]string, 0, 3)

	for _, src := range sources {
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		for _, ns := range parseNameserversFromResolvConf(string(data)) {
			if _, ok := seen[ns]; ok {
				continue
			}
			seen[ns] = struct{}{}
			servers = append(servers, ns)
			if len(servers) >= 3 {
				return servers
			}
		}
	}
	return servers
}

func parseNameserversFromResolvConf(content string) []string {
	seen := map[string]struct{}{}
	servers := make([]string, 0, 2)

	for _, line := range strings.Split(content, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(fields[1])
		if ip == nil || ip.IsLoopback() {
			continue
		}
		key := ip.String()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		servers = append(servers, key)
		if len(servers) >= 3 {
			break
		}
	}
	return servers
}

// staticGuestIPv4ForMAC mirrors the guest agent's staticGuestIPForMAC logic so
// passt's DHCP address and the guest's static fallback converge on the same IP.
func staticGuestIPv4ForMAC(mac, gateway string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(mac)), ":")
	if len(parts) != 6 {
		return "192.168.169.1"
	}
	b4, err4 := strconv.ParseUint(parts[4], 16, 8)
	b5, err5 := strconv.ParseUint(parts[5], 16, 8)
	if err4 != nil || err5 != nil {
		return "192.168.169.1"
	}
	gwParts := strings.SplitN(gateway, ".", 4)
	if len(gwParts) < 2 {
		return fmt.Sprintf("172.26.%d.%d", b4, b5)
	}
	return fmt.Sprintf("%s.%s.%d.%d", gwParts[0], gwParts[1], b4, b5)
}
