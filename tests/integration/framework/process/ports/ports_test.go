/*
Copyright 2026 The Dapr Authors
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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePortRange(t *testing.T) {
	floor, err := parsePortRange([]byte("32768\t60999\n"))
	require.NoError(t, err)
	assert.Equal(t, 32768, floor)

	for _, in := range []string{"", "32768", "abc\t60999\n"} {
		_, err = parsePortRange([]byte(in))
		require.Error(t, err, in)
	}
}

func TestEphemeralCeil(t *testing.T) {
	assert.Equal(t, 16384, ephemeralCeil(16384, nil))
	assert.Equal(t, 32768, ephemeralCeil(32768, nil))
	assert.Equal(t, 49000, ephemeralCeil(49152, nil))
	assert.Equal(t, 49000, ephemeralCeil(1024, nil))
	assert.Equal(t, 49000, ephemeralCeil(0, errors.New("no sysctl")))
}

func TestPortsCeil(t *testing.T) {
	assert.Less(t, portsBase(), portsCeil)
}
