package bundle

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/oursky/nexus/packages/nexus/internal/vm/runner"
	"github.com/spf13/cobra"
)

// ExtractEmbeddedKernel is injected by cmd/nexus (package main) so that the
// bundle run command can extract the platform-specific embedded kernel before
// booting the VM. Nil when running outside the full nexus binary (e.g. tests).
var ExtractEmbeddedKernel func() (string, error)

// runCommand implements `nexus bundle run <bundlepath> [args...]`.
//
// This is invoked by the shell stub embedded in every self-executing NXPACK
// bundle: `exec nexus bundle run "$0" "$@"`.
//
// Behaviour:
//  1. Extract the NXPACK bundle to ~/.cache/nexus/bundles/<hash>/ (idempotent)
//  2. If meta.Bake is non-empty and not yet stamped, run bake inside VM
//  3. Run meta.Up inside the VM (daemonless via libkrun)
func runCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <bundle.nxbundle> [command [args...]]",
		Short: "Run a self-executing workspace bundle",
		Long: `Run a self-executing NXPACK workspace bundle.

The bundle is extracted to ~/.cache/nexus/bundles/<id>/ on first run (idempotent).
Workspace commands run inside an isolated microVM — no nexus daemon required.

If meta.Bake commands are defined and have not yet run for this
extracted bundle, they are executed inside the VM first (one-time setup).
Then meta.Up commands are run inside the VM.

If additional arguments are provided they are executed inside the VM instead
of the workspace.up intent.

This command is typically invoked automatically by the bundle's shell stub:
  ./myworkspace.nxbundle [args...]
`,
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var cpus uint8
			var mem uint32
			parsed := make([]string, 0, len(args))
			for i := 0; i < len(args); i++ {
				switch args[i] {
				case "--cpus":
					if i+1 < len(args) {
						if v, err := strconv.ParseUint(args[i+1], 10, 8); err == nil {
							cpus = uint8(v)
						}
						i++
					}
				case "--memory":
					if i+1 < len(args) {
						if v, err := strconv.ParseUint(args[i+1], 10, 32); err == nil {
							mem = uint32(v)
						}
						i++
					}
				default:
					parsed = append(parsed, args[i])
				}
			}
			if len(parsed) == 0 {
				return fmt.Errorf("bundle run: bundle path is required")
			}
			bundlePath := parsed[0]
			runArgs := parsed[1:]

			// Resolve absolute path early (stub passes $0 which may be relative).
			abs, err := filepath.Abs(bundlePath)
			if err != nil {
				return fmt.Errorf("bundle run: resolve path: %w", err)
			}
			bundlePath = abs

			// Ensure embedded kernel is extracted before booting the VM.
			if ExtractEmbeddedKernel != nil {
				_, _ = ExtractEmbeddedKernel()
			}

			r := runner.Runner{}

			// Extract bundle (idempotent).
			eb, err := r.ExtractBundle(bundlePath)
			if err != nil {
				return fmt.Errorf("bundle run: extract: %w", err)
			}

			// Apply CLI overrides to meta.
			if cpus > 0 {
				eb.Meta.CPUs = cpus
			}
			if mem > 0 {
				eb.Meta.Memory = mem
			}

			ctx := context.Background()
			normalizedUp := normalizeUpCommands(eb.Meta.Up)

			// Discover published ports from docker-compose files and forward them
			// into the VM via gvproxy.
			forwardPorts := discoverBundlePorts(eb.WorkspaceDir)
			if len(forwardPorts) > 0 {
				fmt.Fprintf(os.Stderr, "bundle run: port forwards active: %v\n", forwardPorts)
			}
			r.ForwardPorts = forwardPorts

			stamp := bakeStampPath(eb.WorkspaceDir)
			if needsBake(eb, eb.Meta.Bake, stamp) {
				fmt.Fprintln(os.Stderr, "bundle run: running bake commands (first-time setup)...")
				for _, c := range eb.Meta.Bake {
					fmt.Fprintf(os.Stderr, "bundle run: bake: %s\n", c)
				}
				if len(runArgs) > 0 {
					scriptPath, err := writeRunScript(eb.WorkspaceDir, []string{
						buildBakeScript(eb.Meta.Bake, stamp),
						strings.Join(runArgs, " "),
					})
					if err != nil {
						return err
					}
					if err := r.Run(ctx, eb, []string{"/bin/sh", scriptPath}); err != nil {
						return err
					}
					_ = writeHostStamp(stamp)
					return nil
				}
				if len(normalizedUp) == 0 {
					fmt.Fprintln(os.Stderr, "bundle run: no workspace.up commands defined")
					return nil
				}
				cmds := make([]string, 0, len(eb.Meta.Bake)+len(normalizedUp)+2)
				cmds = append(cmds, buildBakeScript(eb.Meta.Bake, stamp))
				cmds = append(cmds, ensureDockerDaemonCmd())
				cmds = append(cmds, normalizedUp...)
				scriptPath, err := writeRunScript(eb.WorkspaceDir, cmds)
				if err != nil {
					return err
				}
				if err := r.Run(ctx, eb, []string{"/bin/sh", scriptPath}); err != nil {
					return err
				}
				_ = writeHostStamp(stamp)
				return nil
			}

			// If caller passed explicit args, run those inside the VM.
			if len(runArgs) > 0 {
				return r.Run(ctx, eb, runArgs)
			}

			// No args: run workspace.up intent inside the VM.
			// Guard: if Up is empty, print a clear message and exit cleanly.
			if len(normalizedUp) == 0 {
				fmt.Fprintln(os.Stderr, "bundle run: no workspace.up commands defined")
				return nil
			}

			cmds := append([]string{ensureDockerDaemonCmd()}, normalizedUp...)
			scriptPath, err := writeRunScript(eb.WorkspaceDir, cmds)
			if err != nil {
				return err
			}
			return r.Run(ctx, eb, []string{"/bin/sh", scriptPath})
		},
	}
	return cmd
}

// bakeStampPath returns the path to the bake completion stamp file.
func bakeStampPath(workspaceDir string) string {
	return filepath.Join(workspaceDir, ".nexus-baked")
}

// runBake executes workspaceIntent.Bake commands inside the VM if they have
// not already been run for this extracted bundle. The bake stamp file
// (~/.cache/nexus/bundles/<hash>/.baked) prevents re-running on subsequent
// invocations.
func needsBake(eb runner.ExtractedBundle, bake []string, stamp string) bool {
	if len(bake) == 0 {
		return false
	}
	if runtime.GOOS == "linux" {
		rootfsImage := filepath.Join(eb.CacheDir, "rootfs.ext4")
		if hasRootFSStamp(rootfsImage, "/workspace/.nexus-baked") {
			return false
		}
	}
	_, err := os.Stat(stamp)
	return err != nil
}

func hasRootFSStamp(rootfsImage, guestPath string) bool {
	if _, err := os.Stat(rootfsImage); err != nil {
		return false
	}
	cmd := exec.Command("debugfs", "-R", "stat "+guestPath, rootfsImage)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// buildBakeScript returns a single shell command string that runs the bake
// commands (with DNS bootstrap prepended) and touches the bake stamp.
func buildBakeScript(cmds []string, stamp string) string {
	if len(cmds) == 0 {
		return "true"
	}
	// Install iproute2 and iptables so that the run script can bring up eth0
	// with a static IP and Docker can configure bridge networking. libkrunfw
	// does not support DHCP, so we must configure the network manually using
	// `ip`. The custom kernel has CONFIG_BRIDGE and CONFIG_IP_NF_IPTABLES but
	// not CONFIG_NF_TABLES, so we need iptables-legacy (not nft backend).
	// DNS: gvproxy gateway on macOS, passt/DNS forwarder on Linux.
	dnsServer := "192.168.127.1"
	if runtime.GOOS == "linux" {
		dnsServer = "8.8.8.8"
	}
	prefix := fmt.Sprintf("export DEBIAN_FRONTEND=noninteractive TZ=Etc/UTC && mkdir -p /etc /tmp && chown root:root /tmp && chmod 1777 /tmp && printf 'nameserver %s\\n' > /etc/resolv.conf && apt-get update -qq && apt-get install -y --no-install-recommends iproute2 iptables && ln -sf /usr/sbin/iptables-legacy /usr/sbin/iptables && ln -sf /usr/sbin/ip6tables-legacy /usr/sbin/ip6tables", dnsServer)
	parts := make([]string, 0, len(cmds)+2)
	parts = append(parts, prefix)
	for _, c := range cmds {
		parts = append(parts, normalizeBakeCommand(c))
	}
	parts = append(parts, "touch "+strconv.Quote("/workspace/.nexus-baked"))
	return strings.Join(parts, " && ")
}

func buildShellInvocation(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = strconv.Quote(a)
	}
	return strings.Join(quoted, " ")
}

func normalizeBakeCommand(cmd string) string {
	if strings.Contains(cmd, "docker-compose-plugin") {
		// Install official Docker CE static binaries instead of Ubuntu's docker.io
		// package. The Ubuntu package links against libsystemd/libnftables whose
		// socket constructors trigger a TSI hang in libkrun on macOS. The static
		// binaries avoid this entirely.
		return "apt-get install -y --no-install-recommends ca-certificates curl iptables && " +
			"ln -sf /usr/sbin/iptables-legacy /usr/sbin/iptables && ln -sf /usr/sbin/ip6tables-legacy /usr/sbin/ip6tables && " +
			"curl -fsSL https://download.docker.com/linux/static/stable/aarch64/docker-29.1.3.tgz | tar -xz -C /usr/bin --strip-components=1 && " +
			"mkdir -p /usr/libexec/docker/cli-plugins && " +
			"curl -fsSL https://github.com/docker/compose/releases/download/v2.40.3/docker-compose-linux-aarch64 -o /usr/libexec/docker/cli-plugins/docker-compose && " +
			"chmod +x /usr/libexec/docker/cli-plugins/docker-compose"
	}
	return cmd
}

func normalizeUpCommands(cmds []string) []string {
	if len(cmds) == 0 {
		return nil
	}
	out := make([]string, len(cmds))
	for i, c := range cmds {
		n := strings.ReplaceAll(c, "docker-compose", "docker compose")
		if strings.Contains(n, "docker compose") {
			n = "DOCKER_BUILDKIT=0 COMPOSE_DOCKER_CLI_BUILD=0 " + n
		}
		out[i] = n
	}
	return out
}

// writeRunScript writes shell commands to a script file in the workspace
// directory and returns the guest path (e.g. /workspace/.nexus-run.sh).
// Using a script file avoids libkrun kernel command line length limits that
// can trigger InvalidAscii panics with long inline shell commands.
func writeRunScript(workspaceDir string, cmds []string) (string, error) {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nset -e\n")
	// On macOS init.krun does not auto-mount virtiofs shares. Manually mount
	// the workspace share so host changes are visible at /workspace.
	b.WriteString("mkdir -p /workspace && mount -t virtiofs workspace /workspace 2>/dev/null || true\n")
	// Configure virtio-net eth0 with a static IP. libkrunfw does not support
	// DHCP, so we must bring up the interface manually.
	if runtime.GOOS == "darwin" {
		// macOS: gvproxy provides NAT on 192.168.127.0/24.
		b.WriteString("ip addr add 192.168.127.2/24 dev eth0 2>/dev/null || true\n")
		b.WriteString("ip link set eth0 up 2>/dev/null || true\n")
		b.WriteString("ip route add default via 192.168.127.1 2>/dev/null || true\n")
	} else {
		// Linux: passt provides NAT using 10.0.2.0/24 (QEMU user-net model).
		b.WriteString("ip addr add 10.0.2.15/24 dev eth0 2>/dev/null || true\n")
		b.WriteString("ip link set eth0 up 2>/dev/null || true\n")
		b.WriteString("ip route add default via 10.0.2.2 2>/dev/null || true\n")
	}
	b.WriteString("echo 1 > /proc/sys/net/ipv4/ip_forward 2>/dev/null || true\n")
	// Ensure iptables-legacy is used (the custom kernel has CONFIG_BRIDGE and
	// CONFIG_IP_NF_IPTABLES but not CONFIG_NF_TABLES, so nft backend fails).
	b.WriteString("ln -sf /usr/sbin/iptables-legacy /usr/sbin/iptables 2>/dev/null || true\n")
	b.WriteString("ln -sf /usr/sbin/ip6tables-legacy /usr/sbin/ip6tables 2>/dev/null || true\n")
	for _, c := range cmds {
		b.WriteString(c)
		b.WriteByte('\n')
	}
	hostPath := filepath.Join(workspaceDir, ".nexus-run.sh")
	if err := os.WriteFile(hostPath, []byte(b.String()), 0o755); err != nil {
		return "", fmt.Errorf("write run script: %w", err)
	}
	return "/workspace/.nexus-run.sh", nil
}

// writeHostStamp writes the bake completion stamp on the host filesystem.
// On macOS the VM rootfs is ephemeral (merged OCI layers), so the stamp
// created inside the VM does not persist; we write it on the host after
// a successful bake run.
func writeHostStamp(stamp string) error {
	return os.WriteFile(stamp, []byte("ok"), 0o644)
}

func ensureDockerDaemonCmd() string {
	// Docker daemon startup. The custom kernel has CONFIG_BRIDGE and
	// CONFIG_IP_NF_IPTABLES enabled, so default bridge networking works.
	// We keep --ip6tables=false because IPv6 netfilter is not enabled.
	return "if docker info >/dev/null 2>&1; then true; else pkill -9 -x dockerd >/dev/null 2>&1 || true; pkill -9 -x containerd >/dev/null 2>&1 || true; pkill -9 -f containerd-shim >/dev/null 2>&1 || true; rm -rf /var/run/containerd /var/run/docker; rm -f /var/run/docker.pid /var/run/docker.sock; mkdir -p /var/run/containerd /var/run/docker /var/lib/docker /var/lib/docker-exec; echo 1 > /proc/sys/net/ipv4/ip_forward 2>/dev/null || true; nohup dockerd --data-root=/var/lib/docker --exec-root=/var/lib/docker-exec --storage-driver=overlay2 --ip6tables=false >/tmp/nexus-dockerd.log 2>&1 & fi; i=0; until docker info >/dev/null 2>&1; do i=$((i+1)); [ $i -ge 30 ] && { tail -n 80 /tmp/nexus-dockerd.log 2>/dev/null || true; echo 'docker daemon failed to start' >&2; exit 1; }; sleep 1; done"
}
