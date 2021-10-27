package grpc

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/config"
	dapr_testing "github.com/dapr/dapr/pkg/testing"
)

func TestCertRenewal(t *testing.T) {
	t.Run("shouldn't renew", func(t *testing.T) {
		certExpiry := time.Now().Add(time.Hour * 2).UTC()
		certDuration := certExpiry.Sub(time.Now().UTC())

		renew := shouldRenewCert(certExpiry, certDuration)
		assert.False(t, renew)
	})

	t.Run("should renew", func(t *testing.T) {
		certExpiry := time.Now().Add(time.Second * 3).UTC()
		certDuration := certExpiry.Sub(time.Now().UTC())

		time.Sleep(time.Millisecond * 2200)
		renew := shouldRenewCert(certExpiry, certDuration)
		assert.True(t, renew)
	})
}

func TestGetMiddlewareOptions(t *testing.T) {
	t.Run("should enable unary interceptor if tracing and metrics are enabled", func(t *testing.T) {
		fakeServer := &server{
			config: ServerConfig{},
			tracingSpec: config.TracingSpec{
				SamplingRate: "1",
			},
			renewMutex: &sync.Mutex{},
			logger:     logger.NewLogger("dapr.runtime.grpc.test"),
		}

		serverOption := fakeServer.getMiddlewareOptions()

		assert.Equal(t, 1, len(serverOption))
	})

	t.Run("should not disable middleware even when SamplingRate is 0", func(t *testing.T) {
		fakeServer := &server{
			config: ServerConfig{},
			tracingSpec: config.TracingSpec{
				SamplingRate: "0",
			},
			renewMutex: &sync.Mutex{},
			logger:     logger.NewLogger("dapr.runtime.grpc.test"),
		}

		serverOption := fakeServer.getMiddlewareOptions()

		assert.Equal(t, 1, len(serverOption))
	})

	t.Run("should have api access rules middleware", func(t *testing.T) {
		fakeServer := &server{
			config: ServerConfig{},
			tracingSpec: config.TracingSpec{
				SamplingRate: "0",
			},
			renewMutex: &sync.Mutex{},
			logger:     logger.NewLogger("dapr.runtime.grpc.test"),
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:    "state",
						Version: "v1",
					},
				},
			},
		}

		serverOption := fakeServer.getMiddlewareOptions()

		assert.Equal(t, 1, len(serverOption))
	})
}

func TestClose(t *testing.T) {
	port, err := freeport.GetFreePort()
	require.NoError(t, err)
	serverConfig := NewServerConfig("test", "127.0.0.1", port, []string{"127.0.0.1"}, "test", "test", 4, "", 4)
	a := &api{}
	server := NewAPIServer(a, serverConfig, config.TracingSpec{}, config.MetricSpec{}, config.APISpec{}, nil)
	require.NoError(t, server.StartNonBlocking())
	dapr_testing.WaitForListeningAddress(t, 5*time.Second, fmt.Sprintf("127.0.0.1:%d", port))
	assert.NoError(t, server.Close())
}
