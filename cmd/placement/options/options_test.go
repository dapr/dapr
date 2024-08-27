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

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/placement/raft"
)

func TestAppFlag(t *testing.T) {
	opts, err := New([]string{})
	assert.NoError(t, err)
	assert.EqualValues(t, "dapr-placement-0", opts.RaftID)
	assert.EqualValues(t, []raft.PeerInfo{{ID: "dapr-placement-0", Address: "127.0.0.1:8201"}}, opts.RaftPeers)
	assert.EqualValues(t, true, opts.RaftInMemEnabled)
	assert.EqualValues(t, "", opts.RaftLogStorePath)
	assert.EqualValues(t, 50005, opts.PlacementPort)
	assert.EqualValues(t, 8080, opts.HealthzPort)
	assert.EqualValues(t, false, opts.TLSEnabled)
	assert.EqualValues(t, false, opts.MetadataEnabled)
	assert.EqualValues(t, 100, opts.ReplicationFactor)
	assert.EqualValues(t, "localhost", opts.TrustDomain)
	assert.EqualValues(t, "/var/run/secrets/dapr.io/tls/ca.crt", opts.TrustAnchorsFile)
	assert.EqualValues(t, "dapr-sentry.default.svc:443", opts.SentryAddress)
	assert.EqualValues(t, "info", opts.Logger.OutputLevel)
	assert.EqualValues(t, false, opts.Logger.JSONFormatEnabled)
	assert.EqualValues(t, true, opts.Metrics.Enabled())
	assert.EqualValues(t, "9090", opts.Metrics.Port())
	assert.EqualValues(t, 2, opts.KeepAliveTime)
	assert.EqualValues(t, 3, opts.KeepAliveTimeout)
	assert.EqualValues(t, 2, opts.DisseminateTimeout)
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
			assert.NoError(t, err)
			assert.EqualValues(t, tt.out, opts.RaftPeers)
		})
	}
}

func TestBadPlacementKeepAliveTimeEnvVars(t *testing.T) {
	envVarTests := []struct {
		name      string
		envVar    string
		envVarVal string
		error     string
	}{
		{
			"should error if keep alive time is not integer",
			"DAPR_PLACEMENT_KEEPALIVE_TIME",
			"0.5",
			"invalid integer value for the DAPR_PLACEMENT_KEEPALIVE_TIME env variable",
		},
		{
			"should error if keep alive time is not integer",
			"DAPR_PLACEMENT_KEEPALIVE_TIME",
			"abc",
			"invalid integer value for the DAPR_PLACEMENT_KEEPALIVE_TIME env variable",
		},
		{
			"should error if keep alive time is lower than 1",
			"DAPR_PLACEMENT_KEEPALIVE_TIME",
			"0",
			"value should be between 1 and 10 for best performance",
		},
		{
			"should error if keep alive time is higher than 10",
			"DAPR_PLACEMENT_KEEPALIVE_TIME",
			"11",
			"value should be between 1 and 10 for best performance",
		},
		{
			"should error if keep alive timeout is not integer",
			"DAPR_PLACEMENT_KEEPALIVE_TIMEOUT",
			"0.5",
			"invalid integer value for the DAPR_PLACEMENT_KEEPALIVE_TIMEOUT env variable",
		},
		{
			"should error if keep alive timeout is not integer",
			"DAPR_PLACEMENT_KEEPALIVE_TIMEOUT",
			"abc",
			"invalid integer value for the DAPR_PLACEMENT_KEEPALIVE_TIMEOUT env variable",
		},
		{
			"should error if keep alive time is lower than 1",
			"DAPR_PLACEMENT_KEEPALIVE_TIMEOUT",
			"0",
			"value should be between 1 and 10 for best performance",
		},
		{
			"should error if keep alive time is higher than 10",
			"DAPR_PLACEMENT_KEEPALIVE_TIMEOUT",
			"11",
			"value should be between 1 and 10 for best performance",
		},
		{
			"should error if keep alive timeout is not integer",
			"DAPR_PLACEMENT_DISSEMINATE_TIMEOUT",
			"0.5",
			"invalid integer value for the DAPR_PLACEMENT_DISSEMINATE_TIMEOUT env variable",
		},
		{
			"should error if keep alive timeout is not integer",
			"DAPR_PLACEMENT_DISSEMINATE_TIMEOUT",
			"abc",
			"invalid integer value for the DAPR_PLACEMENT_DISSEMINATE_TIMEOUT env variable",
		},
		{
			"should error if keep alive time is lower than 1",
			"DAPR_PLACEMENT_DISSEMINATE_TIMEOUT",
			"0",
			"value should be between 1 and 5 for best performance",
		},
		{
			"should error if keep alive time is higher than 5",
			"DAPR_PLACEMENT_DISSEMINATE_TIMEOUT",
			"6",
			"value should be between 1 and 5 for best performance",
		},
	}

	for _, tt := range envVarTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.envVar, tt.envVarVal)

			opts, err := New([]string{})
			assert.Error(t, err)
			assert.Nil(t, opts)
			assert.ErrorContains(t, err, tt.error)
		})
	}

}

// TestValidPlacementEnvVars tests that valid placement environment variables are accepted
// default values are not covered in this test as they are covered in the TestAppFlag test
func TestValidPlacementEnvVars(t *testing.T) {
	t.Run("valid keep alive time values should be accepted", func(t *testing.T) {
		t.Setenv("DAPR_PLACEMENT_KEEPALIVE_TIME", "1")

		opts, err := New([]string{})
		assert.NoError(t, err)
		assert.NotNil(t, opts)
		assert.EqualValues(t, 1, opts.KeepAliveTime)
	})
	t.Run("valid keep alive timeout values should be accepted", func(t *testing.T) {
		t.Setenv("DAPR_PLACEMENT_KEEPALIVE_TIMEOUT", "3")

		opts, err := New([]string{})
		assert.NoError(t, err)
		assert.NotNil(t, opts)
		assert.EqualValues(t, 3, opts.KeepAliveTimeout)
	})
	t.Run("valid disseminate timeout values should be accepted", func(t *testing.T) {
		t.Setenv("DAPR_PLACEMENT_DISSEMINATE_TIMEOUT", "3")

		opts, err := New([]string{})
		assert.NoError(t, err)
		assert.NotNil(t, opts)
		assert.EqualValues(t, 3, opts.DisseminateTimeout)
	})
}
