package utils

import (
	"net"
	"os"

	"github.com/pkg/errors"
)

const (
	// HostIPEnvVar is the environment variable to override host's chosen IP address.
	HostIPEnvVar = "DAPR_HOST_IP"
)

// GetHostAddress selects a valid outbound IP address for the host.
func GetHostAddress() (string, error) {
	if val, ok := os.LookupEnv(HostIPEnvVar); ok && val != "" {
		return val, nil
	}

	// Use udp so no handshake is made.
	// Any IP can be used, since connection is not established, but we used a known DNS IP.
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		// Could not find one via a  UDP connection, so we fallback to the "old" way: try first non-loopback IPv4:
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			return "", errors.Wrap(err, "error getting interface IP addresses")
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil {
					return ipnet.IP.String(), nil
				}
			}
		}

		return "", errors.New("could not determine host IP address")
	}

	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String(), nil
}
