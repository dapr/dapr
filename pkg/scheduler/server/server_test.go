/*
Copyright 2024 The Dapr Authors
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

package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseClientPorts(t *testing.T) {
	t.Run("parses client ports", func(t *testing.T) {
		ports := []string{
			"scheduler0=5000",
			"scheduler1=5001",
			"scheduler2=5002",
		}

		clientPorts, err := parseClientPorts(ports)
		require.NoError(t, err)
		assert.Len(t, clientPorts, 3)
		assert.Equal(t, "5000", clientPorts["scheduler0"])
		assert.Equal(t, "5001", clientPorts["scheduler1"])
		assert.Equal(t, "5002", clientPorts["scheduler2"])
	})

	t.Run("parses client ports with invalid format", func(t *testing.T) {
		ports := []string{
			"scheduler0=5000",
			"scheduler1=5001",
			"scheduler2",
		}

		_, err := parseClientPorts(ports)
		require.Error(t, err)
	})

	t.Run("trims whitespace", func(t *testing.T) {
		ports := []string{
			" scheduler0=5000 ",
			"scheduler1 = 5001",
		}

		clientPorts, err := parseClientPorts(ports)
		require.NoError(t, err)
		assert.Len(t, clientPorts, 2)
		assert.Equal(t, "5000", clientPorts["scheduler0"])
		assert.Equal(t, "5001", clientPorts["scheduler1"])
	})
}
