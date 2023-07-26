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
	"time"

	"golang.org/x/net/http2"

	contribmiddle "github.com/dapr/components-contrib/middleware"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	httpenpapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	channelhttp "github.com/dapr/dapr/pkg/channel/http"
	compmiddlehttp "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/config/protocol"
	"github.com/dapr/dapr/pkg/grpc"
	middlehttp "github.com/dapr/dapr/pkg/middleware/http"
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

	// MaxRequestBodySize is the maximum request body size.
	MaxRequestBodySize int

	// ReadBufferSize is the read buffer size.
	ReadBufferSize int

	GRPC *grpc.Manager
}

type Channels struct {
	registry            *compmiddlehttp.Registry
	compStore           *compstore.ComponentStore
	meta                *meta.Meta
	appConnectionConfig config.AppConnectionConfig
	tracingSpec         *config.TracingSpec
	maxRequestBodySize  int
	appHTTPPipelineSpec *config.PipelineSpec
	httpClient          *http.Client
	grpc                *grpc.Manager

	appChannel      channel.AppChannel
	enpChannels     map[string]channel.HTTPEndpointAppChannel
	httpEndpChannel channel.AppChannel
}

func New(opts Options) (*Channels, error) {
	c := &Channels{
		registry:            opts.Registry.HTTPMiddlewares(),
		compStore:           opts.ComponentStore,
		meta:                opts.Meta,
		appConnectionConfig: opts.AppConnectionConfig,
		tracingSpec:         opts.GlobalConfig.Spec.TracingSpec,
		maxRequestBodySize:  opts.MaxRequestBodySize,
		appHTTPPipelineSpec: opts.GlobalConfig.Spec.AppHTTPPipelineSpec,
		grpc:                opts.GRPC,
		httpClient:          appHTTPClient(opts.AppConnectionConfig, opts.GlobalConfig, opts.ReadBufferSize),
	}

	// Create a HTTP channel for external HTTP endpoint invocation
	pipeline, err := c.buildHTTPPipelineForSpec(c.appHTTPPipelineSpec, "app channel")
	if err != nil {
		return c, fmt.Errorf("failed to build app HTTP pipeline: %w", err)
	}

	c.httpEndpChannel, err = channelhttp.CreateHTTPChannel(c.appHTTPChannelConfig(pipeline))
	if err != nil {
		return c, fmt.Errorf("failed to create external HTTP app channel: %w", err)
	}

	c.enpChannels, err = c.createHTTPEndpointsChannels(c.appHTTPPipelineSpec)
	if err != nil {
		return c, fmt.Errorf("failed to create HTTP endpoints channels: %w", err)
	}

	if c.appConnectionConfig.Port == 0 {
		log.Warn("App channel is not initialized. Did you configure an app-port?")
		return c, nil
	}

	if c.appConnectionConfig.Protocol.IsHTTP() {
		// Create a HTTP channel
		c.appChannel, err = channelhttp.CreateHTTPChannel(c.appHTTPChannelConfig(pipeline))
		if err != nil {
			return c, fmt.Errorf("failed to create HTTP app channel: %w", err)
		}
		c.appChannel.(*channelhttp.Channel).SetAppHealthCheckPath(c.appConnectionConfig.HealthCheckHTTPPath)
	} else {
		// create gRPC app channel
		c.appChannel, err = c.grpc.GetAppChannel()
		if err != nil {
			return c, fmt.Errorf("failed to create gRPC app channel: %w", err)
		}
	}

	return c, nil
}

func (c *Channels) BuildHTTPPipeline(spec *config.PipelineSpec) (middlehttp.Pipeline, error) {
	return c.buildHTTPPipelineForSpec(spec, "http")
}

func (c *Channels) AppChannel() channel.AppChannel {
	return c.appChannel
}

func (c *Channels) HTTPEndpointsAppChannel() channel.HTTPEndpointAppChannel {
	return c.httpEndpChannel
}

func (c *Channels) EndpointChannels() map[string]channel.HTTPEndpointAppChannel {
	return c.enpChannels
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

// WithAppChannel is used for testing to override the underlying app channel.
func (c *Channels) WithAppChannel(appChannel channel.AppChannel) {
	c.appChannel = appChannel
}

func (c *Channels) buildHTTPPipelineForSpec(spec *config.PipelineSpec, targetPipeline string) (middlehttp.Pipeline, error) {
	if spec == nil {
		return middlehttp.Pipeline{}, nil
	}

	pipeline := middlehttp.Pipeline{
		Handlers: make([]func(next http.Handler) http.Handler, 0, len(spec.Handlers)),
	}

	for _, handlerSpec := range spec.Handlers {
		comp, exists := c.compStore.GetComponent(handlerSpec.Type, handlerSpec.Name)
		if !exists {
			// Log the error but continue with initializing the pipeline
			log.Errorf("couldn't find middleware component defined in configuration with name %s and type %s",
				handlerSpec.Name, handlerSpec.LogName())
			continue
		}

		md := contribmiddle.Metadata{Base: c.meta.ToBaseMetadata(comp)}
		handler, err := c.registry.Create(handlerSpec.Type, handlerSpec.Version, md, handlerSpec.LogName())
		if err != nil {
			err = fmt.Errorf("process component %s error: %w", comp.Name, err)
			if !comp.Spec.IgnoreErrors {
				return middlehttp.Pipeline{}, err
			}
			log.Error(err)
			continue
		}

		log.Infof("enabled %s/%s %s middleware", handlerSpec.Type, targetPipeline, handlerSpec.Version)
		pipeline.Handlers = append(pipeline.Handlers, handler)
	}

	return pipeline, nil
}

func (c *Channels) appHTTPChannelConfig(pipeline middlehttp.Pipeline) channelhttp.ChannelConfiguration {
	conf := channelhttp.ChannelConfiguration{
		CompStore:            c.compStore,
		MaxConcurrency:       c.appConnectionConfig.MaxConcurrency,
		Pipeline:             pipeline,
		TracingSpec:          c.tracingSpec,
		MaxRequestBodySizeMB: c.maxRequestBodySize,
	}

	conf.Endpoint = c.AppHTTPEndpoint()
	conf.Client = c.httpClient

	return conf
}

func (c *Channels) createHTTPEndpointsChannels(spec *config.PipelineSpec) (map[string]channel.HTTPEndpointAppChannel, error) {
	// Create dedicated app channels for known app endpoints
	endpoints := c.compStore.ListHTTPEndpoints()

	if len(endpoints) > 0 {
		channels := make(map[string]channel.HTTPEndpointAppChannel, len(endpoints))

		pipeline, err := c.BuildHTTPPipeline(spec)
		if err != nil {
			return nil, err
		}

		for _, e := range endpoints {
			conf, err := c.getHTTPEndpointAppChannel(pipeline, e)
			if err != nil {
				return nil, err
			}

			ch, err := channelhttp.CreateHTTPChannel(conf)
			if err != nil {
				return nil, err
			}

			channels[e.ObjectMeta.Name] = ch
		}

		return channels, nil
	}

	return nil, nil
}

func (c *Channels) getHTTPEndpointAppChannel(pipeline middlehttp.Pipeline, endpoint httpenpapi.HTTPEndpoint) (channelhttp.ChannelConfiguration, error) {
	conf := channelhttp.ChannelConfiguration{
		CompStore:            c.compStore,
		MaxConcurrency:       c.appConnectionConfig.MaxConcurrency,
		Pipeline:             pipeline,
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
				// For 1.11
				MinVersion: channel.AppChannelMinTLSVersion,
			}
			// TODO: Remove when the feature is finalized
			if globalConfig.IsFeatureEnabled(config.AppChannelAllowInsecureTLS) {
				tlsConfig.MinVersion = 0
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
