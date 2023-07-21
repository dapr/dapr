package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	grpcGo "google.golang.org/grpc"
	grpcMetadata "google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/grpc/metadata"
	"github.com/dapr/dapr/pkg/grpc/universalapi"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	dapr_testing "github.com/dapr/dapr/pkg/testing"
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
			logger: logger.NewLogger("dapr.runtime.grpc.test"),
		}

		serverOption := fakeServer.getMiddlewareOptions()

		assert.Equal(t, 3, len(serverOption))
	})

	t.Run("should not disable middleware even when SamplingRate is 0", func(t *testing.T) {
		fakeServer := &server{
			config: ServerConfig{},
			tracingSpec: config.TracingSpec{
				SamplingRate: "0",
			},
			logger: logger.NewLogger("dapr.runtime.grpc.test"),
		}

		serverOption := fakeServer.getMiddlewareOptions()

		assert.Equal(t, 3, len(serverOption))
	})

	t.Run("should have api access rules middleware", func(t *testing.T) {
		fakeServer := &server{
			config: ServerConfig{},
			tracingSpec: config.TracingSpec{
				SamplingRate: "0",
			},
			logger: logger.NewLogger("dapr.runtime.grpc.test"),
			apiSpec: config.APISpec{
				Allowed: []config.APIAccessRule{
					{
						Name:     "state",
						Version:  "v1",
						Protocol: "grpc",
					},
				},
			},
		}

		serverOption := fakeServer.getMiddlewareOptions()

		assert.Equal(t, 3, len(serverOption))
	})
}

func TestClose(t *testing.T) {
	t.Run("test close with api logging enabled", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		serverConfig := ServerConfig{
			AppID:                "test",
			HostAddress:          "127.0.0.1",
			Port:                 port,
			APIListenAddresses:   []string{"127.0.0.1"},
			NameSpace:            "test",
			TrustDomain:          "test",
			MaxRequestBodySizeMB: 4,
			ReadBufferSizeKB:     4,
			EnableAPILogging:     true,
		}
		a := &api{UniversalAPI: &universalapi.UniversalAPI{CompStore: compstore.New()}}
		server := NewAPIServer(a, serverConfig, config.TracingSpec{}, config.MetricSpec{}, config.APISpec{}, nil, nil)
		require.NoError(t, server.StartNonBlocking())
		dapr_testing.WaitForListeningAddress(t, 5*time.Second, fmt.Sprintf("127.0.0.1:%d", port))
		assert.NoError(t, server.Close())
	})

	t.Run("test close with api logging disabled", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		serverConfig := ServerConfig{
			AppID:                "test",
			HostAddress:          "127.0.0.1",
			Port:                 port,
			APIListenAddresses:   []string{"127.0.0.1"},
			NameSpace:            "test",
			TrustDomain:          "test",
			MaxRequestBodySizeMB: 4,
			ReadBufferSizeKB:     4,
			EnableAPILogging:     false,
		}
		a := &api{UniversalAPI: &universalapi.UniversalAPI{CompStore: compstore.New()}}
		server := NewAPIServer(a, serverConfig, config.TracingSpec{}, config.MetricSpec{}, config.APISpec{}, nil, nil)
		require.NoError(t, server.StartNonBlocking())
		dapr_testing.WaitForListeningAddress(t, 5*time.Second, fmt.Sprintf("127.0.0.1:%d", port))
		assert.NoError(t, server.Close())
	})
}

func Test_server_getGRPCAPILoggingMiddlewares(t *testing.T) {
	logDest := &bytes.Buffer{}
	infoLog := logger.NewLogger("test-api-logging")
	infoLog.EnableJSONOutput(true)
	infoLog.SetOutput(logDest)

	s := &server{
		infoLogger: infoLog,
	}

	dec := json.NewDecoder(logDest)
	called := atomic.Int32{}
	handler := func(ctx context.Context, req any) (any, error) {
		called.Add(1)
		return nil, nil
	}

	logInterceptor, _ := s.getGRPCAPILoggingMiddlewares()

	runTest := func(userAgent string) func(t *testing.T) {
		md := grpcMetadata.MD{}
		if userAgent != "" {
			md["user-agent"] = []string{userAgent}
		}
		ctx := grpcMetadata.NewIncomingContext(context.Background(), md)

		info := &grpcGo.UnaryServerInfo{
			FullMethod: "/dapr.proto.runtime.v1.Dapr/GetState",
		}
		return func(t *testing.T) {
			metadata.SetMetadataInContextUnary(ctx, nil, info, func(ctx context.Context, req any) (any, error) {
				return logInterceptor(ctx, req, info, handler)
			})

			logData := map[string]string{}
			err := dec.Decode(&logData)
			require.NoError(t, err)

			assert.Equal(t, "test-api-logging", logData["scope"])
			assert.Equal(t, "gRPC API Called", logData["msg"])
			assert.Equal(t, "/dapr.proto.runtime.v1.Dapr/GetState", logData["method"])
			if userAgent != "" {
				assert.Equal(t, userAgent, logData["useragent"])
			} else {
				_, found := logData["useragent"]
				assert.False(t, found)
			}
		}
	}

	t.Run("without user agent", runTest(""))
	t.Run("with user agent", runTest("daprtest/1"))
}
