/*
Copyright 2025 The Dapr Authors
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
package roundrobin

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stretchr/testify/assert"
)

func TestNewStaticConnector(t *testing.T) {
	addresses := []string{"dapr1:50005", "dapr2:50005", "dapr3:50005"}
	conn, err := NewStaticConnector(StaticOptions{Addresses: addresses})

	require.NoError(t, err)

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "dapr1:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "dapr2:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "dapr3:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "dapr1:50005", conn.Address())
}
