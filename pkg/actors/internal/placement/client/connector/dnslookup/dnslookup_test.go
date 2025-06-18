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
package dnslookup

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lookupHost(addrs []string) lookupFunc {
	return func(ctx context.Context, host string) ([]string, error) {
		return addrs, nil
	}
}

func TestNewDNSConnector(t *testing.T) {
	conn, err := New(Options{
		Address:  "dapr-placement-server.dapr-tests.svc.cluster.local:50005",
		resolver: lookupHost([]string{"add1", "add2", "add3"}),
	})
	require.NoError(t, err)

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "add1:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "add2:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "add3:50005", conn.Address())

	_, _ = conn.Connect(t.Context())
	assert.Equal(t, "add1:50005", conn.Address())
}

func TestNewDNSConnectorErrors(t *testing.T) {
	_, err := New(Options{
		Address: "dns:///dapr-placement-server.dapr-tests.svc.cluster.local:50005",
	})
	assert.Error(t, err)
}
