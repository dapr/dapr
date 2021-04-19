package grpc

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/logger"
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
}
