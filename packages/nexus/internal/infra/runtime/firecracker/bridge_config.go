package firecracker

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultBridgeSubnet  = "172.26.0.0/16"
	defaultBridgeGateway = "172.26.0.1"

	// legacyBridgeSubnetFile is the old privileged-setup path (no longer written).
	legacyBridgeSubnetFile = "/var/lib/nexus/bridge-subnet"
)

// bridgeSubnetFile returns the user-scoped subnet file path under XDG_DATA_HOME.
func bridgeSubnetFile() string {
	if s := os.Getenv("XDG_DATA_HOME"); s != "" {
		return filepath.Join(s, "nexus", "rootless", "bridge-subnet")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return legacyBridgeSubnetFile
	}
	return filepath.Join(home, ".local", "share", "nexus", "rootless", "bridge-subnet")
}

// bridgeSubnet returns the configured bridge subnet CIDR.
// Priority: NEXUS_BRIDGE_SUBNET env var → user-scoped state file → legacy file → compiled default.
func bridgeSubnet() string {
	if s := os.Getenv("NEXUS_BRIDGE_SUBNET"); s != "" {
		return s
	}
	if data, err := os.ReadFile(bridgeSubnetFile()); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return s
		}
	}
	// Migration: read legacy /var/lib/nexus location if present.
	if data, err := os.ReadFile(legacyBridgeSubnetFile); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return s
		}
	}
	return defaultBridgeSubnet
}

// bridgeGatewayIP returns the gateway IP (first host) in the bridge subnet.
func bridgeGatewayIP() string {
	_, ipNet, err := net.ParseCIDR(bridgeSubnet())
	if err != nil {
		return defaultBridgeGateway
	}
	ip4 := ipNet.IP.To4()
	if ip4 == nil {
		return defaultBridgeGateway
	}
	return fmt.Sprintf("%d.%d.%d.1", ip4[0], ip4[1], ip4[2])
}

// guestSubnetCIDR returns the CIDR for the Firecracker VM guest network.
func guestSubnetCIDR() string {
	return bridgeSubnet()
}

// vmNetworkGatewayIP returns the gateway IP that the guest VM should use as its
// default route. In rootless (slirp4netns) mode this is "10.0.2.2" (the gateway
// that slirp4netns always places at <cidr>.2 of its default 10.0.2.0/24 subnet).
// On non-Linux hosts the bridge gateway is used for consistency with tests.
var vmNetworkGatewayIP = defaultVMNetworkGatewayIP

// guestIPForCID derives a guest VM IP within the active network's subnet,
// using the first two octets of the gateway and the CID as the host portion.
func guestIPForCID(cid uint32) string {
	gw := vmNetworkGatewayIP()
	parts := strings.SplitN(gw, ".", 4)
	if len(parts) >= 2 {
		return fmt.Sprintf("%s.%s.%d.%d", parts[0], parts[1],
			byte((cid>>8)&0xFF), byte(cid&0xFF))
	}
	return fmt.Sprintf("172.26.%d.%d", byte((cid>>8)&0xFF), byte(cid&0xFF))
}
