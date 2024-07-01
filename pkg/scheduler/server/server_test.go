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
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/modes"
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

func TestServerConf(t *testing.T) {
	t.Run("KubernetesMode", func(t *testing.T) {
		opts := Options{
			Security:                nil,
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.KubernetesMode,
			ReplicaCount:            0,
			ReplicaID:               0,
			DataDir:                 "",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://localhost:5001", "id2=http://localhost:5002"},
			EtcdClientPorts:         []string{"id1=5001", "id2=5002"},
			EtcdClientHTTPPorts:     []string{"id1=5003", "id2=5004"},
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
		}

		s, err := New(opts)
		if err != nil {
			t.Fatalf("failed to create server: %s", err)
		}

		config := s.config

		assert.Equal(t, "id1=http://localhost:5001,id2=http://localhost:5002", config.InitialCluster)

		clientURL := url.URL{
			Scheme: "http",
			Host:   "0.0.0.0:5002",
		}

		assert.Equal(t, clientURL, config.ListenPeerUrls[0])
		assert.Equal(t, clientURL, config.ListenClientUrls[0])

		clientHTTPURL := url.URL{
			Scheme: "http",
			Host:   "0.0.0.0:5004",
		}
		assert.Equal(t, clientHTTPURL, config.ListenClientHttpUrls[0])
	})

	t.Run("StandaloneMode", func(t *testing.T) {
		opts := Options{
			Security:                nil,
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.StandaloneMode,
			ReplicaCount:            0,
			ReplicaID:               0,
			DataDir:                 "./data",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://localhost:5001", "id2=http://localhost:5002"},
			EtcdClientPorts:         []string{"id1=5001", "id2=5002"},
			EtcdClientHTTPPorts:     []string{"id1=5003", "id2=5004"},
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
		}

		s, err := New(opts)
		if err != nil {
			t.Fatalf("failed to create server: %s", err)
		}

		config := s.config

		assert.Equal(t, "id1=http://localhost:5001,id2=http://localhost:5002", config.InitialCluster)
		assert.Equal(t, "./data-default-id2", config.Dir)

		clientURL := url.URL{
			Scheme: "http",
			Host:   "localhost:5002",
		}

		assert.Equal(t, clientURL, config.ListenPeerUrls[0])
		assert.Equal(t, clientURL, config.ListenClientUrls[0])

		clientHTTPURL := url.URL{
			Scheme: "http",
			Host:   "localhost:5004",
		}
		assert.Equal(t, clientHTTPURL, config.ListenClientHttpUrls[0])
	})

	t.Run("StandaloneMode without client http ports", func(t *testing.T) {
		opts := Options{
			Security:                nil,
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.StandaloneMode,
			ReplicaCount:            0,
			ReplicaID:               0,
			DataDir:                 "./data",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://localhost:5001", "id2=http://localhost:5002"},
			EtcdClientPorts:         []string{"id1=5001", "id2=5002"},
			EtcdClientHTTPPorts:     nil,
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
		}

		s, err := New(opts)
		if err != nil {
			t.Fatalf("failed to create server: %s", err)
		}

		config := s.config

		assert.Equal(t, "id1=http://localhost:5001,id2=http://localhost:5002", config.InitialCluster)

		clientURL := url.URL{
			Scheme: "http",
			Host:   "localhost:5002",
		}

		assert.Equal(t, clientURL, config.ListenPeerUrls[0])
		assert.Equal(t, clientURL, config.ListenClientUrls[0])

		assert.Empty(t, config.ListenClientHttpUrls)
	})
}
