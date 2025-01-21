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
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security/fake"
)

func TestServerConf(t *testing.T) {
	t.Run("KubernetesMode", func(t *testing.T) {
		config, err := config(Options{
			Security:                fake.New(),
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.KubernetesMode,
			DataDir:                 "",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://localhost:5001", "id2=http://localhost:5002"},
			EtcdClientPorts:         []string{"id1=5001", "id2=5002"},
			EtcdClientHTTPPorts:     []string{"id1=5003", "id2=5004"},
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
			Healthz:                 healthz.New(),
		})
		require.NoError(t, err)

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
		config, err := config(Options{
			Security:                fake.New(),
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.StandaloneMode,
			DataDir:                 "./data",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://localhost:5001", "id2=http://localhost:5002"},
			EtcdClientPorts:         []string{"id1=5001", "id2=5002"},
			EtcdClientHTTPPorts:     []string{"id1=5003", "id2=5004"},
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
			Healthz:                 healthz.New(),
		})
		require.NoError(t, err)

		assert.Equal(t, "id1=http://localhost:5001,id2=http://localhost:5002", config.InitialCluster)
		if runtime.GOOS == "windows" {
			assert.Equal(t, "data\\default-id2", config.Dir)
		} else {
			assert.Equal(t, "data/default-id2", config.Dir)
		}

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
		config, err := config(Options{
			Security:                fake.New(),
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.StandaloneMode,
			DataDir:                 "./data",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://localhost:5001", "id2=http://localhost:5002"},
			EtcdClientPorts:         []string{"id1=5001", "id2=5002"},
			EtcdClientHTTPPorts:     nil,
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
			Healthz:                 healthz.New(),
		})
		require.NoError(t, err)

		assert.Equal(t, "id1=http://localhost:5001,id2=http://localhost:5002", config.InitialCluster)

		clientURL := url.URL{
			Scheme: "http",
			Host:   "localhost:5002",
		}

		assert.Equal(t, clientURL, config.ListenPeerUrls[0])
		assert.Equal(t, clientURL, config.ListenClientUrls[0])

		assert.Empty(t, config.ListenClientHttpUrls)
	})

	t.Run("StandaloneMode listen on 0.0.0.0 when a host", func(t *testing.T) {
		config, err := config(Options{
			Security:                fake.New(),
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.StandaloneMode,
			DataDir:                 "./data",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://hello1:5001", "id2=http://hello2:5002"},
			EtcdClientPorts:         []string{"id1=5001", "id2=5002"},
			EtcdClientHTTPPorts:     nil,
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
			Healthz:                 healthz.New(),
		})
		require.NoError(t, err)

		assert.Equal(t, "id1=http://hello1:5001,id2=http://hello2:5002", config.InitialCluster)

		clientURL := url.URL{
			Scheme: "http",
			Host:   "0.0.0.0:5002",
		}
		assert.Equal(t, clientURL, config.ListenPeerUrls[0])
		assert.Equal(t, clientURL, config.ListenClientUrls[0])
		assert.Empty(t, config.ListenClientHttpUrls)
	})

	t.Run("StandaloneMode listen on IP when an IP", func(t *testing.T) {
		config, err := config(Options{
			Security:                fake.New(),
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.StandaloneMode,
			DataDir:                 "./data",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://1.2.3.4:5001", "id2=http://1.2.3.4:5002"},
			EtcdClientPorts:         []string{"id1=5001", "id2=5002"},
			EtcdClientHTTPPorts:     nil,
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
			Healthz:                 healthz.New(),
		})
		require.NoError(t, err)

		assert.Equal(t, "id1=http://1.2.3.4:5001,id2=http://1.2.3.4:5002", config.InitialCluster)

		clientURL := url.URL{
			Scheme: "http",
			Host:   "1.2.3.4:5002",
		}
		assert.Equal(t, clientURL, config.ListenPeerUrls[0])
		assert.Equal(t, clientURL, config.ListenClientUrls[0])
		assert.Empty(t, config.ListenClientHttpUrls)
	})

	t.Run("StandaloneMode listen on HTTP IP when an IP", func(t *testing.T) {
		config, err := config(Options{
			Security:                fake.New(),
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.StandaloneMode,
			DataDir:                 "./data",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://1.2.3.4:5001", "id2=http://1.2.3.4:5002"},
			EtcdClientPorts:         []string{"id1=5001", "id2=5002"},
			EtcdClientHTTPPorts:     []string{"id1=5003", "id2=5004"},
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
			Healthz:                 healthz.New(),
		})
		require.NoError(t, err)

		assert.Equal(t, "id1=http://1.2.3.4:5001,id2=http://1.2.3.4:5002", config.InitialCluster)

		clientURL := url.URL{
			Scheme: "http",
			Host:   "1.2.3.4:5002",
		}
		assert.Equal(t, clientURL, config.ListenPeerUrls[0])
		assert.Equal(t, clientURL, config.ListenClientUrls[0])
		httpURL, err := url.Parse("http://1.2.3.4:5004")
		require.NoError(t, err)
		assert.Equal(t, []url.URL{*httpURL}, config.ListenClientHttpUrls)
	})

	t.Run("expect error when giving bad client ports", func(t *testing.T) {
		config, err := config(Options{
			Security:                fake.New(),
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.StandaloneMode,
			DataDir:                 "./data",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://1.2.3.4:5001", "id2=http://1.2.3.4:5002"},
			EtcdClientPorts:         []string{"id15001"},
			EtcdClientHTTPPorts:     nil,
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
			Healthz:                 healthz.New(),
		})
		require.Error(t, err)
		assert.Nil(t, config)
	})

	t.Run("expect error when giving bad HTTP client ports", func(t *testing.T) {
		config, err := config(Options{
			Security:                fake.New(),
			ListenAddress:           "",
			Port:                    0,
			Mode:                    modes.StandaloneMode,
			DataDir:                 "./data",
			EtcdID:                  "id2",
			EtcdInitialPeers:        []string{"id1=http://1.2.3.4:5001", "id2=http://1.2.3.4:5002"},
			EtcdClientPorts:         []string{"id=5001"},
			EtcdClientHTTPPorts:     []string{"id15001"},
			EtcdSpaceQuota:          0,
			EtcdCompactionMode:      "",
			EtcdCompactionRetention: "",
			Healthz:                 healthz.New(),
		})
		require.Error(t, err)
		assert.Nil(t, config)
	})
}
