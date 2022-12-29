/*
Copyright 2022 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"errors"
	"fmt"
	"net"
	"os"
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
			return "", fmt.Errorf("error getting interface IP addresses: %w", err)
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
