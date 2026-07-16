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
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetHostAdress(t *testing.T) {
	t.Run("DAPR_HOST_IP present", func(t *testing.T) {
		hostIP := "test.local"
		t.Setenv(HostIPEnvVar, hostIP)

		address, err := GetHostAddress()
		require.NoError(t, err)
		assert.Equal(t, hostIP, address)
	})

	t.Run("DAPR_HOST_IP not present, non-empty response", func(t *testing.T) {
		address, err := GetHostAddress()
		require.NoError(t, err)
		assert.NotEmpty(t, address)
	})
}

// fakeConn implements net.Conn with a configurable LocalAddr.
type fakeConn struct {
	net.Conn
	addr net.Addr
}

func (c *fakeConn) LocalAddr() net.Addr { return c.addr }
func (c *fakeConn) Close() error        { return nil }

// fakeIPNet returns a *net.IPNet with the given IP and a full mask.
func fakeIPNet(ip net.IP) *net.IPNet {
	bits := 32
	if ip.To4() == nil {
		bits = 128
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
}

func noEnv(string) (string, bool) { return "", false }

func failDial(string, string) (net.Conn, error) {
	return nil, errors.New("no route")
}

func dialReturning(ip net.IP) func(string, string) (net.Conn, error) {
	return func(string, string) (net.Conn, error) {
		return &fakeConn{addr: &net.UDPAddr{IP: ip}}, nil
	}
}

// dialReturningOn succeeds only when the target matches addr, otherwise fails.
func dialReturningOn(addr string, ip net.IP) func(string, string) (net.Conn, error) {
	return func(_, target string) (net.Conn, error) {
		if target == addr {
			return &fakeConn{addr: &net.UDPAddr{IP: ip}}, nil
		}
		return nil, errors.New("no route")
	}
}

func ifaceAddrs(ips ...net.IP) func() ([]net.Addr, error) {
	return func() ([]net.Addr, error) {
		addrs := make([]net.Addr, len(ips))
		for i, ip := range ips {
			addrs[i] = fakeIPNet(ip)
		}
		return addrs, nil
	}
}

func noIfaces() ([]net.Addr, error) { return nil, nil }

func TestGetHostAddressDetailed(t *testing.T) {
	tests := []struct {
		name       string
		lookupEnv  func(string) (string, bool)
		dial       func(string, string) (net.Conn, error)
		ifaceAddrs func() ([]net.Addr, error)
		want       string
		wantErr    bool
	}{
		{
			name:       "env var override",
			lookupEnv:  func(key string) (string, bool) { return "1.2.3.4", true },
			dial:       failDial,
			ifaceAddrs: noIfaces,
			want:       "1.2.3.4",
		},
		{
			name:       "env var empty is ignored",
			lookupEnv:  func(key string) (string, bool) { return "", true },
			dial:       dialReturning(net.ParseIP("10.0.0.1")),
			ifaceAddrs: noIfaces,
			want:       "10.0.0.1",
		},
		{
			name:       "dial returns private IPv4",
			lookupEnv:  noEnv,
			dial:       dialReturning(net.ParseIP("10.0.0.5")),
			ifaceAddrs: noIfaces,
			want:       "10.0.0.5",
		},
		{
			name:       "dial returns IPv6 GUA",
			lookupEnv:  noEnv,
			dial:       dialReturning(net.ParseIP("2600::1")),
			ifaceAddrs: noIfaces,
			want:       "2600::1",
		},
		{
			name:       "IPv6-only: IPv4 dial fails, IPv6 dial succeeds",
			lookupEnv:  noEnv,
			dial:       dialReturningOn("[2001:db8::1]:80", net.ParseIP("2600::1")),
			ifaceAddrs: noIfaces,
			want:       "2600::1",
		},
		{
			name:      "dial returns CLAT address, falls through to interfaces",
			lookupEnv: noEnv,
			dial:      dialReturning(net.ParseIP("192.0.0.1")), // inside 192.0.0.0/29
			ifaceAddrs: ifaceAddrs(
				net.ParseIP("10.0.0.1"),
			),
			want: "10.0.0.1",
		},
		{
			name:      "dial fails, fallback picks public IPv4 over private",
			lookupEnv: noEnv,
			dial:      failDial,
			ifaceAddrs: ifaceAddrs(
				net.ParseIP("10.0.0.1"),    // private → prio 2
				net.ParseIP("203.0.113.5"), // public → prio 0
			),
			want: "203.0.113.5",
		},
		{
			name:      "fallback prefers IPv6 GUA over private IPv4",
			lookupEnv: noEnv,
			dial:      failDial,
			ifaceAddrs: ifaceAddrs(
				net.ParseIP("10.0.0.1"), // private IPv4 → prio 2
				net.ParseIP("2600::1"),  // GUA → prio 1
			),
			want: "2600::1",
		},
		{
			name:      "fallback prefers private IPv4 over ULA",
			lookupEnv: noEnv,
			dial:      failDial,
			ifaceAddrs: ifaceAddrs(
				net.ParseIP("fd12::1"),     // ULA → prio 3
				net.ParseIP("192.168.1.1"), // private IPv4 → prio 2
			),
			want: "192.168.1.1",
		},
		{
			name:      "fallback prefers CGNAT over ULA",
			lookupEnv: noEnv,
			dial:      failDial,
			ifaceAddrs: ifaceAddrs(
				net.ParseIP("fd12::1"),    // ULA → prio 3
				net.ParseIP("100.64.0.1"), // CGNAT → prio 2
			),
			want: "100.64.0.1",
		},
		{
			name:      "fallback uses link-local as last resort",
			lookupEnv: noEnv,
			dial:      failDial,
			ifaceAddrs: ifaceAddrs(
				net.ParseIP("fe80::1"),
			),
			want: "fe80::1",
		},
		{
			name:      "fallback skips loopback",
			lookupEnv: noEnv,
			dial:      failDial,
			ifaceAddrs: ifaceAddrs(
				net.ParseIP("127.0.0.1"),
				net.ParseIP("10.0.0.1"),
			),
			want: "10.0.0.1",
		},
		{
			name:      "fallback skips CLAT addresses",
			lookupEnv: noEnv,
			dial:      failDial,
			ifaceAddrs: ifaceAddrs(
				net.ParseIP("192.0.0.1"), // CLAT → skipped
				net.ParseIP("10.0.0.1"),
			),
			want: "10.0.0.1",
		},
		{
			name:       "no addresses available returns error",
			lookupEnv:  noEnv,
			dial:       failDial,
			ifaceAddrs: noIfaces,
			wantErr:    true,
		},
		{
			name:      "only loopback returns error",
			lookupEnv: noEnv,
			dial:      failDial,
			ifaceAddrs: ifaceAddrs(
				net.ParseIP("127.0.0.1"),
				net.ParseIP("::1"),
			),
			wantErr: true,
		},
		{
			name:      "InterfaceAddrs error propagates",
			lookupEnv: noEnv,
			dial:      failDial,
			ifaceAddrs: func() ([]net.Addr, error) {
				return nil, errors.New("network subsystem failure")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getHostAddress(tt.lookupEnv, tt.dial, tt.ifaceAddrs)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAddrPriority(t *testing.T) {
	tests := []struct {
		name string
		ip   string
		want int
	}{
		// Public IPv4 → 0
		{"public IPv4", "203.0.113.5", 0},
		{"public IPv4 8.8.8.8", "8.8.8.8", 0},

		// IPv6 GUA → 1
		{"IPv6 GUA", "2001:db8::1", 1},
		{"IPv6 GUA 2600::", "2600::1", 1},

		// Private IPv4 → 2
		{"private 10.x", "10.0.0.1", 2},
		{"private 172.16.x", "172.16.0.1", 2},
		{"private 192.168.x", "192.168.1.1", 2},

		// CGNAT → 2
		{"CGNAT low", "100.64.0.1", 2},
		{"CGNAT high", "100.127.255.254", 2},

		// IPv6 ULA → 3
		{"IPv6 ULA fd", "fd12::1", 3},

		// Link-local → 99
		{"link-local IPv4", "169.254.1.1", 99},
		{"link-local IPv6", "fe80::1", 99},

		// Skipped → -1
		{"multicast IPv6", "ff02::1", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			require.NotNil(t, ip, "failed to parse IP %q", tt.ip)
			assert.Equal(t, tt.want, addrPriority(ip))
		})
	}
}

func TestIsCGNAT(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		// Inside 100.64.0.0/10
		{"100.64.0.0", true},
		{"100.64.0.1", true},
		{"100.100.100.100", true},
		{"100.127.255.255", true},

		// Outside 100.64.0.0/10
		{"100.63.255.255", false},
		{"100.128.0.0", false},
		{"10.0.0.1", false},
		{"192.168.1.1", false},

		// IPv6 → always false
		{"2001:db8::1", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			require.NotNil(t, ip)
			assert.Equal(t, tt.want, isCGNAT(ip))
		})
	}
}
