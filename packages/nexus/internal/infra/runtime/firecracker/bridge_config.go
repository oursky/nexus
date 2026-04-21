package firecracker

import (
	"fmt"
	"net"
	"os"
	"strings"
)

const (
	defaultBridgeSubnet  = "172.26.0.0/16"
	defaultBridgeGateway = "172.26.0.1"
	bridgeSubnetFile     = "/var/lib/nexus/bridge-subnet"
)

// bridgeSubnet returns the configured bridge subnet CIDR.
// Priority: NEXUS_BRIDGE_SUBNET env var → persisted file → compiled default.
func bridgeSubnet() string {
	if s := os.Getenv("NEXUS_BRIDGE_SUBNET"); s != "" {
		return s
	}
	if data, err := os.ReadFile(bridgeSubnetFile); err == nil {
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

// guestIPForCID derives a guest VM IP from the vsock CID within the bridge subnet.
func guestIPForCID(cid uint32) string {
	gw := bridgeGatewayIP()
	parts := strings.SplitN(gw, ".", 4)
	if len(parts) >= 2 {
		return fmt.Sprintf("%s.%s.%d.%d", parts[0], parts[1],
			byte((cid>>8)&0xFF), byte(cid&0xFF))
	}
	return fmt.Sprintf("172.26.%d.%d", byte((cid>>8)&0xFF), byte(cid&0xFF))
}
