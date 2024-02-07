/*
Copyright 2023 The Dapr Authors
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

package channels

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/dapr/dapr/pkg/api/grpc/manager"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	httpendpapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	channelhttp "github.com/dapr/dapr/pkg/channel/http"
	compmiddlehttp "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/config/protocol"
	"github.com/dapr/dapr/pkg/middleware"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.channels")

type Options struct {
	// Registry is the all-component registry.
	Registry *registry.Registry

	// ComponentStore is the component store.
	ComponentStore *compstore.ComponentStore

	// Metadata is the metadata helper.
	Meta *meta.Meta

	// AppConnectionConfig is the application connection configuration.
	AppConnectionConfig config.AppConnectionConfig

	// GlobalConfig is the global configuration.
	GlobalConfig *config.Configuration

	// AppMiddlware is the application middleware.
	AppMiddleware middleware.HTTP

	// MaxRequestBodySize is the maximum request body size.
	MaxRequestBodySize int

	// ReadBufferSize is the read buffer size.
	ReadBufferSize int

	GRPC *manager.Manager
}

type Channels struct {
	registry            *compmiddlehttp.Registry
	compStore           *compstore.ComponentStore
	meta                *meta.Meta
	appConnectionConfig config.AppConnectionConfig
	tracingSpec         *config.TracingSpec
	maxRequestBodySize  int
	appMiddlware        middleware.HTTP
	httpClient          *http.Client
	grpc                *manager.Manager

	appChannel      channel.AppChannel
	endpChannels    map[string]channel.HTTPEndpointAppChannel
	httpEndpChannel channel.AppChannel
	lock            sync.RWMutex
}

func New(opts Options) *Channels {
	return &Channels{
		registry:            opts.Registry.HTTPMiddlewares(),
		compStore:           opts.ComponentStore,
		meta:                opts.Meta,
		appConnectionConfig: opts.AppConnectionConfig,
		tracingSpec:         opts.GlobalConfig.Spec.TracingSpec,
		maxRequestBodySize:  opts.MaxRequestBodySize,
		appMiddlware:        opts.AppMiddleware,
		grpc:                opts.GRPC,
		httpClient:          appHTTPClient(opts.AppConnectionConfig, opts.GlobalConfig, opts.ReadBufferSize),
		endpChannels:        make(map[string]channel.HTTPEndpointAppChannel),
	}
}

func (c *Channels) Refresh() error {
	c.lock.Lock()
	defer c.lock.Unlock()

	log.Debug("Refreshing channels")

	httpEndpChannel, err := channelhttp.CreateHTTPChannel(c.appHTTPChannelConfig())
	if err != nil {
		return fmt.Errorf("failed to create external HTTP app channel: %w", err)
	}

	endpChannels, err := c.initEndpointChannels()
	if err != nil {
		return fmt.Errorf("failed to create HTTP endpoints channels: %w", err)
	}

	c.httpEndpChannel = httpEndpChannel
	c.endpChannels = endpChannels

	if c.appConnectionConfig.Port == 0 {
		log.Warn("App channel is not initialized. Did you configure an app-port?")
		return nil
	}

	var appChannel channel.AppChannel
	if c.appConnectionConfig.Protocol.IsHTTP() {
		// Create a HTTP channel
		appChannel, err = channelhttp.CreateHTTPChannel(c.appHTTPChannelConfig())
		if err != nil {
			return fmt.Errorf("failed to create HTTP app channel: %w", err)
		}
		appChannel.(*channelhttp.Channel).SetAppHealthCheckPath(c.appConnectionConfig.HealthCheckHTTPPath)
	} else {
		// create gRPC app channel
		appChannel, err = c.grpc.GetAppChannel()
		if err != nil {
			return fmt.Errorf("failed to create gRPC app channel: %w", err)
		}
	}

	c.appChannel = appChannel
	log.Debug("Channels refreshed")

	return nil
}

func (c *Channels) AppChannel() channel.AppChannel {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.appChannel
}

func (c *Channels) HTTPEndpointsAppChannel() channel.HTTPEndpointAppChannel {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.httpEndpChannel
}

func (c *Channels) EndpointChannels() map[string]channel.HTTPEndpointAppChannel {
	c.lock.RLock()
	defer c.lock.RUnlock()
	return c.endpChannels
}

func (c *Channels) AppHTTPClient() *http.Client {
	return c.httpClient
}

// AppHTTPEndpoint Returns the HTTP endpoint for the app.
func (c *Channels) AppHTTPEndpoint() string {
	// Application protocol is "http" or "https"
	port := strconv.Itoa(c.appConnectionConfig.Port)
	switch c.appConnectionConfig.Protocol {
	case protocol.HTTPProtocol, protocol.H2CProtocol:
		return "http://" + c.appConnectionConfig.ChannelAddress + ":" + port
	case protocol.HTTPSProtocol:
		return "https://" + c.appConnectionConfig.ChannelAddress + ":" + port
	default:
		return ""
	}
}

func (c *Channels) appHTTPChannelConfig() channelhttp.ChannelConfiguration {
	conf := channelhttp.ChannelConfiguration{
		CompStore:            c.compStore,
		MaxConcurrency:       c.appConnectionConfig.MaxConcurrency,
		Middleware:           c.appMiddlware,
		TracingSpec:          c.tracingSpec,
		MaxRequestBodySizeMB: c.maxRequestBodySize,
	}

	conf.Endpoint = c.AppHTTPEndpoint()
	conf.Client = c.httpClient

	return conf
}

func (c *Channels) initEndpointChannels() (map[string]channel.HTTPEndpointAppChannel, error) {
	// Create dedicated app channels for known app endpoints
	endpoints := c.compStore.ListHTTPEndpoints()

	channels := make(map[string]channel.HTTPEndpointAppChannel, len(endpoints))
	if len(endpoints) > 0 {
		for _, e := range endpoints {
			conf, err := c.getHTTPEndpointAppChannel(e)
			if err != nil {
				return nil, err
			}

			ch, err := channelhttp.CreateHTTPChannel(conf)
			if err != nil {
				return nil, err
			}

			channels[e.ObjectMeta.Name] = ch
		}
	}

	return channels, nil
}

func (c *Channels) getHTTPEndpointAppChannel(endpoint httpendpapi.HTTPEndpoint) (channelhttp.ChannelConfiguration, error) {
	conf := channelhttp.ChannelConfiguration{
		CompStore:            c.compStore,
		MaxConcurrency:       c.appConnectionConfig.MaxConcurrency,
		Middleware:           c.appMiddlware,
		MaxRequestBodySizeMB: c.maxRequestBodySize,
		TracingSpec:          c.tracingSpec,
	}

	var tlsConfig *tls.Config

	if endpoint.HasTLSRootCA() {
		ca := endpoint.Spec.ClientTLS.RootCA.Value.String()
		caCertPool := x509.NewCertPool()

		if !caCertPool.AppendCertsFromPEM([]byte(ca)) {
			return channelhttp.ChannelConfiguration{}, fmt.Errorf("failed to add root cert to cert pool for http endpoint %s", endpoint.ObjectMeta.Name)
		}

		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    caCertPool,
		}
	}

	if endpoint.HasTLSPrivateKey() {
		cert, err := tls.X509KeyPair([]byte(endpoint.Spec.ClientTLS.Certificate.Value.String()), []byte(endpoint.Spec.ClientTLS.PrivateKey.Value.String()))
		if err != nil {
			return channelhttp.ChannelConfiguration{}, fmt.Errorf("failed to load client certificate for http endpoint %s: %w", endpoint.ObjectMeta.Name, err)
		}

		if tlsConfig == nil {
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	if endpoint.Spec.ClientTLS != nil && endpoint.Spec.ClientTLS.Renegotiation != nil {
		switch *endpoint.Spec.ClientTLS.Renegotiation {
		case commonapi.NegotiateNever:
			tlsConfig.Renegotiation = tls.RenegotiateNever
		case commonapi.NegotiateOnceAsClient:
			tlsConfig.Renegotiation = tls.RenegotiateOnceAsClient
		case commonapi.NegotiateFreelyAsClient:
			tlsConfig.Renegotiation = tls.RenegotiateFreelyAsClient
		default:
			return channelhttp.ChannelConfiguration{}, fmt.Errorf("invalid renegotiation value %s for http endpoint %s", *endpoint.Spec.ClientTLS.Renegotiation, endpoint.ObjectMeta.Name)
		}
	}

	dialer := &net.Dialer{
		Timeout: 15 * time.Second,
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSHandshakeTimeout = 15 * time.Second
	tr.TLSClientConfig = tlsConfig
	tr.DialContext = dialer.DialContext

	conf.Client = &http.Client{
		Timeout:   0,
		Transport: tr,
	}

	return conf, nil
}

// appHTTPClient Initializes the appHTTPClient property.
func appHTTPClient(connConfig config.AppConnectionConfig, globalConfig *config.Configuration, readBufferSize int) *http.Client {
	var transport http.RoundTripper

	if connConfig.Protocol == protocol.H2CProtocol {
		// Enable HTTP/2 Cleartext transport
		transport = &http2.Transport{
			AllowHTTP: true, // To enable using "http" as protocol
			DialTLS: func(network, addr string, _ *tls.Config) (net.Conn, error) {
				// Return the TCP socket without TLS
				return net.Dial(network, addr)
			},
			// TODO: This may not be exactly the same as "MaxResponseHeaderBytes" so check before enabling this
			// MaxHeaderListSize: uint32(a.runtimeConfig.readBufferSize << 10),
		}
	} else {
		var tlsConfig *tls.Config
		if connConfig.Protocol == protocol.HTTPSProtocol {
			tlsConfig = &tls.Config{
				InsecureSkipVerify: true, //nolint:gosec
				MinVersion:         channel.AppChannelMinTLSVersion,
			}
		}

		transport = &http.Transport{
			TLSClientConfig:        tlsConfig,
			ReadBufferSize:         readBufferSize << 10,
			MaxResponseHeaderBytes: int64(readBufferSize) << 10,
			MaxConnsPerHost:        1024,
			MaxIdleConns:           64, // A local channel connects to a single host
			MaxIdleConnsPerHost:    64,
		}
	}

	// Initialize this property in the object, and then pass it to the HTTP channel and the actors runtime (for health checks)
	// We want to re-use the same client so TCP sockets can be re-used efficiently across everything that communicates with the app
	// This is especially useful if the app supports HTTP/2
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
