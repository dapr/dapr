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
