package server

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/modes"
)

func TestParseClientPorts(t *testing.T) {
	t.Run("parses client ports", func(t *testing.T) {
		ports := []string{
			"scheduler0=5000",
			"scheduler1=5001",
			"scheduler2=5002",
		}

		clientPorts := parseClientPorts(ports)
		assert.Equal(t, 3, len(clientPorts))
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

		clientPorts := parseClientPorts(ports)
		assert.Equal(t, 2, len(clientPorts))
		assert.Equal(t, "5000", clientPorts["scheduler0"])
		assert.Equal(t, "5001", clientPorts["scheduler1"])
	})

	t.Run("trims whitespace", func(t *testing.T) {
		ports := []string{
			" scheduler0=5000 ",
			"scheduler1 = 5001",
		}

		clientPorts := parseClientPorts(ports)
		assert.Equal(t, 2, len(clientPorts))
		assert.Equal(t, "5000", clientPorts["scheduler0"])
		assert.Equal(t, "5001", clientPorts["scheduler1"])
	})
}

func TestServerConf(t *testing.T) {
	t.Run("KubernetesMode", func(t *testing.T) {
		s := &Server{
			mode:                modes.KubernetesMode,
			id:                  "id2",
			etcdClientPorts:     map[string]string{"id1": "5001", "id2": "5002"},
			etcdClientHttpPorts: map[string]string{"id1": "5003", "id2": "5004"},
			etcdInitialPeers:    []string{"id1=http://localhost:5001", "id2=http://localhost:5002"},
		}

		config := s.conf()

		assert.Equal(t, "id1=http://localhost:5001,id2=http://localhost:5002", config.InitialCluster)

		clientUrl := url.URL{
			Scheme: "http",
			Host:   "0.0.0.0:5002",
		}

		assert.Equal(t, clientUrl, config.ListenPeerUrls[0])
		assert.Equal(t, clientUrl, config.ListenClientUrls[0])

		clientHttpUrl := url.URL{
			Scheme: "http",
			Host:   "0.0.0.0:5004",
		}
		assert.Equal(t, clientHttpUrl, config.ListenClientHttpUrls[0])
	})

	t.Run("DefaultMode", func(t *testing.T) {
		s := &Server{
			mode:                modes.StandaloneMode,
			id:                  "id2",
			dataDir:             "./data",
			etcdClientPorts:     map[string]string{"id1": "5001", "id2": "5002"},
			etcdClientHttpPorts: map[string]string{"id1": "5003", "id2": "5004"},
			etcdInitialPeers:    []string{"id1=http://localhost:5001", "id2=http://localhost:5002"},
		}

		config := s.conf()

		assert.Equal(t, "id1=http://localhost:5001,id2=http://localhost:5002", config.InitialCluster)
		assert.Equal(t, "./data-default-id2", config.Dir)

		clientUrl := url.URL{
			Scheme: "http",
			Host:   "localhost:5002",
		}

		assert.Equal(t, clientUrl, config.ListenPeerUrls[0])
		assert.Equal(t, clientUrl, config.ListenClientUrls[0])

		clientHttpUrl := url.URL{
			Scheme: "http",
			Host:   "localhost:5004",
		}
		assert.Equal(t, clientHttpUrl, config.ListenClientHttpUrls[0])
	})

	t.Run("DefaultMode without client http ports", func(t *testing.T) {
		s := &Server{
			mode:             modes.StandaloneMode,
			id:               "id2",
			etcdClientPorts:  map[string]string{"id1": "5001", "id2": "5002"},
			etcdInitialPeers: []string{"id1=http://localhost:5001", "id2=http://localhost:5002"},
		}

		config := s.conf()

		assert.Equal(t, "id1=http://localhost:5001,id2=http://localhost:5002", config.InitialCluster)

		clientUrl := url.URL{
			Scheme: "http",
			Host:   "localhost:5002",
		}

		assert.Equal(t, clientUrl, config.ListenPeerUrls[0])
		assert.Equal(t, clientUrl, config.ListenClientUrls[0])

		assert.Empty(t, config.ListenClientHttpUrls)
	})
}
