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

package external

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	streamutils "github.com/dapr/dapr/utils/streams"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// HTTPStatusCode is an dapr http channel status code.
	HTTPStatusCode = "http.status_code"
	httpScheme     = "http"
	httpsScheme    = "https"
)

// HTTPEndpointAppChannel is an HTTP implementation of an AppChannel.
type HTTPEndpointAppChannel struct {
	client                *http.Client
	ch                    chan struct{}
	compStore             *compstore.ComponentStore
	tracingSpec           config.TracingSpec
	appHeaderToken        string
	maxResponseBodySizeMB int
	appHealthCheckPath    string
	appHealth             *apphealth.AppHealth
	pipeline              httpMiddleware.Pipeline
}

// ChannelConfigurationForHTTPEndpoints is the configuration used to create an HTTP AppChannel for external service invocation.
type ChannelConfigurationForHTTPEndpoints struct {
	Client               *http.Client
	CompStore            *compstore.ComponentStore
	MaxConcurrency       int
	Pipeline             httpMiddleware.Pipeline
	TracingSpec          config.TracingSpec
	MaxRequestBodySizeMB int
}

// CreateNonLocalChannel creates an HTTP AppChannel for external service invocation.
func CreateNonLocalChannel(config ChannelConfigurationForHTTPEndpoints) (channel.HTTPEndpointAppChannel, error) {
	c := &HTTPEndpointAppChannel{
		pipeline:              config.Pipeline,
		client:                config.Client,
		compStore:             config.CompStore,
		tracingSpec:           config.TracingSpec,
		appHeaderToken:        auth.GetAppToken(),
		maxResponseBodySizeMB: config.MaxRequestBodySizeMB,
	}

	if config.MaxConcurrency > 0 {
		c.ch = make(chan struct{}, config.MaxConcurrency)
	}

	return c, nil
}

// InvokeMethod invokes user code via HTTP.
// InvokeMethod for HTTPEndpointAppChannel will invoke the external application
func (h *HTTPEndpointAppChannel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (rsp *invokev1.InvokeMethodResponse, err error) {
	// Check if HTTP Extension is given. Otherwise, it will return error.
	httpExt := req.Message().GetHttpExtension()
	if httpExt == nil {
		return nil, status.Error(codes.InvalidArgument, "missing HTTP extension field")
	}
	// Go's net/http library does not support sending requests with the CONNECT method
	if httpExt.Verb == commonv1pb.HTTPExtension_NONE || httpExt.Verb == commonv1pb.HTTPExtension_CONNECT { //nolint:nosnakecase
		return nil, status.Error(codes.InvalidArgument, "invalid HTTP verb")
	}

	switch req.APIVersion() {
	case internalv1pb.APIVersion_V1: //nolint:nosnakecase
		rsp, err = h.invokeMethodV1(ctx, req, appID)

	default:
		// Reject unsupported version
		err = status.Error(codes.Unimplemented, fmt.Sprintf("Unsupported spec version: %d", req.APIVersion()))
	}

	return rsp, err
}

// invokeMethodV1 constructs the http endpoint request and performs the request while collecting metrics.
func (h *HTTPEndpointAppChannel) invokeMethodV1(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error) {
	channelReq, err := h.constructRequest(ctx, req, appID)
	if err != nil {
		return nil, err
	}

	if h.ch != nil {
		h.ch <- struct{}{}
	}
	defer func() {
		if h.ch != nil {
			<-h.ch
		}
	}()

	// Emit metric when request is sent
	diag.DefaultHTTPMonitoring.ClientRequestStarted(ctx, channelReq.Method, req.Message().Method, int64(len(req.Message().Data.GetValue())))
	startRequest := time.Now()

	var resp *http.Response
	if len(h.pipeline.Handlers) > 0 {
		// Exec pipeline only if at least one handler is specified
		rw := &rwRecorder{
			w: &bytes.Buffer{},
		}
		execPipeline := h.pipeline.Apply(http.HandlerFunc(func(wr http.ResponseWriter, r *http.Request) {
			// Send request to user application
			// (Body is closed below, but linter isn't detecting that)
			//nolint:bodyclose
			clientResp, clientErr := h.client.Do(r)
			if clientResp != nil {
				copyHeader(wr.Header(), clientResp.Header)
				wr.WriteHeader(clientResp.StatusCode)
				_, _ = io.Copy(wr, clientResp.Body)
			}
			if clientErr != nil {
				err = clientErr
			}
		}))
		execPipeline.ServeHTTP(rw, channelReq)
		resp = rw.Result() //nolint:bodyclose
	} else {
		// Send request to user application
		// (Body is closed below, but linter isn't detecting that)
		//nolint:bodyclose
		resp, err = h.client.Do(channelReq)
	}

	elapsedMs := float64(time.Since(startRequest) / time.Millisecond)

	var contentLength int64
	if resp != nil {
		if resp.Header != nil {
			contentLength, _ = strconv.ParseInt(resp.Header.Get("content-length"), 10, 64)
		}
	}

	if err != nil {
		diag.DefaultHTTPMonitoring.ClientRequestCompleted(ctx, channelReq.Method, req.Message().GetMethod(), strconv.Itoa(http.StatusInternalServerError), contentLength, elapsedMs)
		return nil, err
	}

	rsp, err := h.parseChannelResponse(req, resp)
	if err != nil {
		diag.DefaultHTTPMonitoring.ClientRequestCompleted(ctx, channelReq.Method, req.Message().GetMethod(), strconv.Itoa(http.StatusInternalServerError), contentLength, elapsedMs)
		return nil, err
	}

	diag.DefaultHTTPMonitoring.ClientRequestCompleted(ctx, channelReq.Method, req.Message().GetMethod(), strconv.Itoa(int(rsp.Status().Code)), contentLength, elapsedMs)

	return rsp, nil
}

func (h *HTTPEndpointAppChannel) getBaseURL(appID string) string {
	for _, endpoint := range h.compStore.ListHTTPEndpoints() {
		if appID == endpoint.Name {
			return endpoint.Spec.BaseURL
		}
	}
	return ""
}

func (h *HTTPEndpointAppChannel) constructRequest(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*http.Request, error) {
	// Construct app channel URI: VERB http://api.github.com/method?query1=value1

	msg := req.Message()
	verb := msg.HttpExtension.Verb.String()
	method := msg.Method
	uri := strings.Builder{}

	// check for overwritten baseURL set as appID.
	// otherwise, get baseURL from http endpoint CRDs.
	if strings.Contains(appID, "://") {
		uri.WriteString(appID)
	} else {
		uri.WriteString(h.getBaseURL(appID))
	}

	if len(method) > 0 && method[0] != '/' {
		uri.WriteRune('/')
	}
	uri.WriteString(method)

	qs := req.EncodeHTTPQueryString()
	if qs != "" {
		uri.WriteRune('?')
		uri.WriteString(qs)
	}

	channelReq, err := http.NewRequestWithContext(ctx, verb, uri.String(), req.RawData())
	if err != nil {
		return nil, err
	}

	// Recover headers
	invokev1.InternalMetadataToHTTPHeader(ctx, req.Metadata(), channelReq.Header.Add)
	channelReq.Header.Set("content-type", req.ContentType())

	// Configure headers from http endpoint CRD
	// TODO(@Sam): use get here instead
	for _, endpoint := range h.compStore.ListHTTPEndpoints() {
		if endpoint.Name == appID {
			for _, header := range endpoint.Spec.Headers {
				channelReq.Header.Set(header.Name, header.Value.String())
			}
		}
	}

	// HTTP client needs to inject traceparent header for proper tracing stack.
	span := diagUtils.SpanFromContext(ctx)
	tp := diag.SpanContextToW3CString(span.SpanContext())
	ts := diag.TraceStateToW3CString(span.SpanContext())
	channelReq.Header.Set("traceparent", tp)
	if ts != "" {
		channelReq.Header.Set("tracestate", ts)
	}

	if h.appHeaderToken != "" {
		channelReq.Header.Set(authConsts.APITokenHeader, h.appHeaderToken)
	}

	return channelReq, nil
}

func (h *HTTPEndpointAppChannel) parseChannelResponse(req *invokev1.InvokeMethodRequest, channelResp *http.Response) (*invokev1.InvokeMethodResponse, error) {
	contentType := channelResp.Header.Get("content-type")

	// Limit response body if needed
	var body io.ReadCloser
	if h.maxResponseBodySizeMB > 0 {
		body = streamutils.LimitReadCloser(channelResp.Body, int64(h.maxResponseBodySizeMB)<<20)
	} else {
		body = channelResp.Body
	}

	// Convert status code
	rsp := invokev1.
		NewInvokeMethodResponse(int32(channelResp.StatusCode), "", nil).
		WithHTTPHeaders(channelResp.Header).
		WithRawData(body).
		WithContentType(contentType)

	return rsp, nil
}

// HealthProbe performs a health probe.
func (h *HTTPEndpointAppChannel) HealthProbe(ctx context.Context) (bool, error) {
	// TODO(@Sam): delete this

	return true, nil
}

func copyHeader(dst http.Header, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// SetAppHealthCheckPath sets the path where to send requests for health probes.
func (h *HTTPEndpointAppChannel) SetAppHealthCheckPath(path string) {
	h.appHealthCheckPath = "/" + strings.TrimPrefix(path, "/")
}

// SetAppHealth sets the apphealth.AppHealth object.
// TODO(@Sam): delete this?
func (h *HTTPEndpointAppChannel) SetAppHealth(ah *apphealth.AppHealth) {
	h.appHealth = ah
}
