/*
Copyright 2021 The Dapr Authors
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

package options

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/placement/raft"
)

func TestAppFlag(t *testing.T) {
	opts, err := New([]string{})
	require.NoError(t, err)
	assert.EqualValues(t, "dapr-placement-0", opts.RaftID)
	assert.EqualValues(t, []raft.PeerInfo{{ID: "dapr-placement-0", Address: "127.0.0.1:8201"}}, opts.RaftPeers)
	assert.True(t, opts.RaftInMemEnabled)
	assert.EqualValues(t, "", opts.RaftLogStorePath)
	assert.EqualValues(t, 50005, opts.PlacementPort)
	assert.EqualValues(t, 8080, opts.HealthzPort)
	assert.False(t, opts.TLSEnabled)
	assert.False(t, opts.MetadataEnabled)
	assert.EqualValues(t, 100, opts.ReplicationFactor)
	assert.EqualValues(t, "localhost", opts.TrustDomain)
	assert.EqualValues(t, "/var/run/secrets/dapr.io/tls/ca.crt", opts.TrustAnchorsFile)
	assert.EqualValues(t, "dapr-sentry.default.svc:443", opts.SentryAddress)
	assert.EqualValues(t, "info", opts.Logger.OutputLevel)
	assert.False(t, opts.Logger.JSONFormatEnabled)
	assert.True(t, opts.Metrics.Enabled())
	assert.EqualValues(t, "9090", opts.Metrics.Port())
	assert.EqualValues(t, 2*time.Second, opts.KeepAliveTime)
	assert.EqualValues(t, 3*time.Second, opts.KeepAliveTimeout)
	assert.EqualValues(t, 2*time.Second, opts.DisseminateTimeout)
}

func TestInitialCluster(t *testing.T) {
	peerAddressTests := []struct {
		name string
		in   []string
		out  []raft.PeerInfo
	}{
		{
			"one address",
			[]string{
				"--initial-cluster", "node0=127.0.0.1:3030",
			},
			[]raft.PeerInfo{
				{ID: "node0", Address: "127.0.0.1:3030"},
			},
		}, {
			"three addresses in two flags",
			[]string{
				"--initial-cluster", "node0=127.0.0.1:3030",
				"--initial-cluster", "node1=127.0.0.1:3031,node2=127.0.0.1:3032",
			},
			[]raft.PeerInfo{
				{ID: "node0", Address: "127.0.0.1:3030"},
				{ID: "node1", Address: "127.0.0.1:3031"},
				{ID: "node2", Address: "127.0.0.1:3032"},
			},
		}, {
			"one address is invalid",
			[]string{
				"--initial-cluster", "127.0.0.1:3030,node1=127.0.0.1:3031,node2=127.0.0.1:3032",
			},
			[]raft.PeerInfo{
				{ID: "node1", Address: "127.0.0.1:3031"},
				{ID: "node2", Address: "127.0.0.1:3032"},
			},
		},
	}

	for _, tt := range peerAddressTests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := New(tt.in)
			require.NoError(t, err)
			assert.EqualValues(t, tt.out, opts.RaftPeers)
		})
	}
}

func TestOptsFromEnvVariables(t *testing.T) {
	t.Run("metadata enabled", func(t *testing.T) {
		t.Setenv("DAPR_PLACEMENT_METADATA_ENABLED", "true")

		opts, err := New([]string{})
		require.NoError(t, err)
		assert.True(t, opts.MetadataEnabled)
	})
}

func TestValidateFlags(t *testing.T) {
	testCases := []struct {
		name  string
		arg   string
		value string
	}{
		{
			"keepalive-time too low",
			"keepalive-time",
			"0.5s",
		},
		{
			"keepalive-time too high",
			"keepalive-time",
			"11s",
		},
		{
			"keepalive-timeout too low",
			"keepalive-timeout",
			"0.5s",
		},
		{
			"keepalive-timeout too high",
			"keepalive-timeout",
			"11s",
		},
		{
			"disseminate-timeout too low",
			"disseminate-timeout",
			"0.5s",
		},
		{
			"disseminate-timeout too high",
			"disseminate-timeout",
			"6s",
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New([]string{"--" + tt.arg, tt.value})
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid value for "+tt.arg)
		})
	}
}
