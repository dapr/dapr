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
		assert.True(t, (port1 >= 10233 && port1 <= 12280) || (port1 >= 22444 && port1 <= 24491))

		port2 := getPort(t, "myapp")
		assert.Equal(t, port1, port2)
	})

	t.Run("Invoking with a different appID returns a different port", func(t *testing.T) {
		port1 := getPort(t, "myapp1")
		assert.True(t, (port1 >= 10233 && port1 <= 12280) || (port1 >= 22444 && port1 <= 24491))

		port2 := getPort(t, "myapp2")
		assert.True(t, (port2 >= 10233 && port2 <= 12280) || (port2 >= 22444 && port2 <= 24491))
		assert.NotEqual(t, port1, port2)
	})

	t.Run("returns a random port if the stable one is busy", func(t *testing.T) {
		port1 := getPort(t, "myapp1")
		assert.True(t, (port1 >= 10233 && port1 <= 12280) || (port1 >= 22444 && port1 <= 24491))

		addr, err := net.ResolveTCPAddr("tcp", "localhost:"+strconv.Itoa(port1))
		require.NoError(t, err)
		l, err := net.ListenTCP("tcp", addr)
		require.NoError(t, err)
		defer l.Close()

		port2 := getPort(t, "myapp1")
		assert.NotEqual(t, port1, port2)
	})
}
