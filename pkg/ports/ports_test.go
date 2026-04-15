/*
Copyright 2023 The Dapr Authors
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

package ports

import (
	"fmt"
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStablePort(t *testing.T) {
	// Try twice; if there's an error, use a different starting port to avoid conflicts
	getPort := func(t *testing.T, appID string) int {
		t.Helper()

		startPort := 10233
	getport:
		port, err := GetStablePort(startPort, appID)
		if err != nil && startPort == 10233 {
			startPort = 22444
			goto getport
		}

		require.NoError(t, err)

		return port
	}

	t.Run("invoking twice should return the same port", func(t *testing.T) {
		port1 := getPort(t, "myapp")
		assert.True(t, port1 > 0 && port1 <= 65535)

		port2 := getPort(t, "myapp")
		assert.Equal(t, port1, port2)
	})

	t.Run("Invoking with different appIDs returns different ports", func(t *testing.T) {
		ports := make(map[int]bool)
		for i := range 10 {
			port := getPort(t, fmt.Sprintf("different-app-%d", i))
			ports[port] = true
		}
		assert.Len(t, ports, 10,
			"all 10 different app IDs should produce different stable ports")
	})

	t.Run("returns a random port if the stable one is busy", func(t *testing.T) {
		port1 := getPort(t, "myapp1")
		assert.True(t, port1 > 0 && port1 <= 65535)

		addr, err := net.ResolveTCPAddr("tcp", "localhost:"+strconv.Itoa(port1))
		require.NoError(t, err)
		l, err := net.ListenTCP("tcp", addr)
		require.NoError(t, err)
		defer l.Close()

		port2 := getPort(t, "myapp1")
		assert.NotEqual(t, port1, port2)
	})

	t.Run("high start port stays within valid TCP range", func(t *testing.T) {
		port, err := GetStablePort(47300, "high-start-app")
		require.NoError(t, err)
		assert.True(t, port > 0 && port <= 65535,
			"port %d should be within valid TCP range", port)
	})
}
