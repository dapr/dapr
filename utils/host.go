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
// It first tries a UDP dial to documentation addresses to discover the
// kernel-preferred outbound address, then falls back to enumerating
// interface addresses ordered by preference:
//  1. Public IPv4
//  2. IPv6 GUA (Global Unicast)
//  3. Private IPv4 (RFC 1918, CGNAT)
//  4. IPv6 ULA
//  5. Link-Local Addresses
func GetHostAddress() (string, error) {
	return getHostAddress(os.LookupEnv, net.Dial, net.InterfaceAddrs)
}

func getHostAddress(
	lookupEnv func(string) (string, bool),
	dial func(string, string) (net.Conn, error),
	interfaceAddrs func() ([]net.Addr, error),
) (string, error) {
	if val, ok := lookupEnv(HostIPEnvVar); ok && val != "" {
		return val, nil
	}

	_, clatSubnet, _ := net.ParseCIDR("192.0.0.0/29")
	ipByDial := func(a string) (string, bool) {
		conn, err := dial("udp", a)
		if err != nil {
			return "", false
		}
		defer conn.Close()

		udpAddr, ok := conn.LocalAddr().(*net.UDPAddr)
		if !ok || udpAddr == nil || udpAddr.IP == nil {
			return "", false
		}
		if clatSubnet.Contains(udpAddr.IP) {
			return "", false
		}
		return udpAddr.IP.String(), true
	}

	// Try UDP dial to documentation addresses (no packets are sent).
	// This asks the kernel routing table for the preferred source address.
	for _, a := range []string{"192.0.2.1:80", "[2001:db8::1]:80"} {
		if ip, ok := ipByDial(a); ok {
			return ip, nil
		}
	}

	// Fallback: enumerate interface addresses sorted by priority.
	addrs, err := interfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("error getting interface IP addresses: %w", err)
	}

	var best net.IP
	bestPrio := 100

	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || clatSubnet.Contains(ipnet.IP) {
			continue
		}

		ip := ipnet.IP
		prio := addrPriority(ip)
		if prio < 0 {
			continue
		}
		if best == nil || prio < bestPrio {
			best = ip
			bestPrio = prio
		}
	}

	if best != nil {
		return best.String(), nil
	}

	return "", errors.New("could not determine host IP address")
}

// addrPriority returns a sort priority for the given IP address.
// Lower values are preferred. Returns -1 if the address should be skipped.
func addrPriority(ip net.IP) int {
	// Use Link Local as last resort
	if ip.IsLinkLocalUnicast() {
		return 99
	}
	if ip.To4() != nil {
		if ip.IsPrivate() || isCGNAT(ip) {
			return 2 // private IPv4 (RFC 1918) or shared (RFC 6598)
		}
		return 0 // public IPv4
	}
	// IPv6
	if ip.IsPrivate() {
		return 3 // ULA (fc00::/7)
	}
	if ip.IsGlobalUnicast() {
		return 1 // GUA (2000::/3)
	}
	return -1 // skip everything else
}

// isCGNAT reports whether ip is in the RFC 6598 Shared Address Space
// (100.64.0.0/10). Go's net.IP.IsPrivate() does not cover this range.
func isCGNAT(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 100 && ip4[1]&0xc0 == 64
}

func IsLocalhost(addr string) bool {
	return addr == "localhost" || addr == "127.0.0.1" || addr == "[::1]" || addr == "::1"
}
