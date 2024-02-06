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

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/middleware"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/security"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	streamutils "github.com/dapr/kit/streams"
)

const (
	// HTTPStatusCode is an dapr http channel status code.
	HTTPStatusCode = "http.status_code"
	httpScheme     = "http"
	httpsScheme    = "https"

	appConfigEndpoint = "dapr/config"
)

// Channel is an HTTP implementation of an AppChannel.
type Channel struct {
	client                *http.Client
	baseAddress           string
	ch                    chan struct{}
	compStore             *compstore.ComponentStore
	tracingSpec           *config.TracingSpec
	appHeaderToken        string
	maxResponseBodySizeMB int
	appHealthCheckPath    string
	appHealth             *apphealth.AppHealth
	middleware            middleware.HTTP
}

// ChannelConfiguration is the configuration used to create an HTTP AppChannel.
type ChannelConfiguration struct {
	Client               *http.Client
	CompStore            *compstore.ComponentStore
	Endpoint             string
	MaxConcurrency       int
	Middleware           middleware.HTTP
	TracingSpec          *config.TracingSpec
	MaxRequestBodySizeMB int
	TLSClientCert        string
	TLSClientKey         string
	TLSRootCA            string
	TLSRenegotiation     string
}

// CreateHTTPChannel creates an HTTP AppChannel.
func CreateHTTPChannel(config ChannelConfiguration) (channel.AppChannel, error) {
	c := &Channel{
		middleware:            config.Middleware,
		client:                config.Client,
		compStore:             config.CompStore,
		baseAddress:           config.Endpoint,
		tracingSpec:           config.TracingSpec,
		appHeaderToken:        security.GetAppToken(),
		maxResponseBodySizeMB: config.MaxRequestBodySizeMB,
	}

	if config.MaxConcurrency > 0 {
		c.ch = make(chan struct{}, config.MaxConcurrency)
	}

	return c, nil
}

// GetAppConfig gets application config from user application
// GET http://localhost:<app_port>/dapr/config
func (h *Channel) GetAppConfig(ctx context.Context, appID string) (*config.ApplicationConfig, error) {
	req := invokev1.NewInvokeMethodRequest(appConfigEndpoint).
		WithHTTPExtension(http.MethodGet, "").
		WithContentType(invokev1.JSONContentType).
		WithMetadata(map[string][]string{
			"dapr-app-id": {appID},
		})
	defer req.Close()

	resp, err := h.InvokeMethod(ctx, req, "")
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	var config config.ApplicationConfig

	if resp.Status().GetCode() != http.StatusOK {
		return &config, nil
	}

	// Get versioning info, currently only v1 is supported.
	headers := resp.Headers()
	var version string
	if val, ok := headers["dapr-app-config-version"]; ok && len(val.GetValues()) > 0 {
		version = val.GetValues()[0]
	}

	switch version {
	case "v1":
		fallthrough
	default:
		err = json.NewDecoder(resp.RawData()).Decode(&config)
		if err != nil {
			return nil, err
		}
	}

	return &config, nil
}

// InvokeMethod invokes user code via HTTP.
func (h *Channel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error) {
	// Check if HTTP Extension is given. Otherwise, it will return error.
	httpExt := req.Message().GetHttpExtension()
	if httpExt == nil {
		return nil, status.Error(codes.InvalidArgument, "missing HTTP extension field")
	}
	// Go's net/http library does not support sending requests with the CONNECT method
	if httpExt.GetVerb() == commonv1pb.HTTPExtension_NONE || httpExt.GetVerb() == commonv1pb.HTTPExtension_CONNECT { //nolint:nosnakecase
		return nil, status.Error(codes.InvalidArgument, "invalid HTTP verb")
	}

	// If the request is for an internal endpoint, do not allow it if the app health status is not successful
	if h.baseAddress != "" && appID == "" && h.appHealth != nil && h.appHealth.GetStatus() != apphealth.AppStatusHealthy {
		return nil, status.Error(codes.Internal, messages.ErrAppUnhealthy)
	}

	switch req.APIVersion() {
	case internalv1pb.APIVersion_V1: //nolint:nosnakecase
		return h.invokeMethodV1(ctx, req, appID)
	}

	// Reject unsupported version
	return nil, status.Error(codes.Unimplemented, fmt.Sprintf("Unsupported spec version: %d", req.APIVersion()))
}

// SetAppHealthCheckPath sets the path where to send requests for health probes.
func (h *Channel) SetAppHealthCheckPath(path string) {
	h.appHealthCheckPath = "/" + strings.TrimPrefix(path, "/")
}

// SetAppHealth sets the apphealth.AppHealth object.
func (h *Channel) SetAppHealth(ah *apphealth.AppHealth) {
	h.appHealth = ah
}

// HealthProbe performs a health probe.
func (h *Channel) HealthProbe(ctx context.Context) (bool, error) {
	channelReq, err := http.NewRequestWithContext(ctx, http.MethodGet, h.baseAddress+h.appHealthCheckPath, nil)
	if err != nil {
		return false, err
	}

	diag.DefaultHTTPMonitoring.AppHealthProbeStarted(ctx)
	startRequest := time.Now()

	channelResp, err := h.client.Do(channelReq)

	elapsedMs := float64(time.Since(startRequest) / time.Millisecond)

	if err != nil {
		// Errors here are network-level errors, so we are not returning them as errors
		// Instead, we just return a failed probe
		diag.DefaultHTTPMonitoring.AppHealthProbeCompleted(ctx, strconv.Itoa(http.StatusInternalServerError), elapsedMs)
		//nolint:nilerr
		return false, nil
	}

	// Drain before closing
	_, _ = io.Copy(io.Discard, channelResp.Body)
	channelResp.Body.Close()

	status := channelResp.StatusCode >= 200 && channelResp.StatusCode < 300
	diag.DefaultHTTPMonitoring.AppHealthProbeCompleted(ctx, strconv.Itoa(channelResp.StatusCode), elapsedMs)

	return status, nil
}

func (h *Channel) invokeMethodV1(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*invokev1.InvokeMethodResponse, error) {
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
	diag.DefaultHTTPMonitoring.ClientRequestStarted(ctx, channelReq.Method, req.Message().GetMethod(), int64(len(req.Message().GetData().GetValue())))
	startRequest := time.Now()

	rw := &RWRecorder{
		W: &bytes.Buffer{},
	}
	execPipeline := h.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Send request to user application
		// (Body is closed below, but linter isn't detecting that)
		//nolint:bodyclose
		clientResp, clientErr := h.client.Do(r)
		if clientResp != nil {
			copyHeader(w.Header(), clientResp.Header)
			w.WriteHeader(clientResp.StatusCode)
			_, _ = io.Copy(w, clientResp.Body)
		}
		if clientErr != nil {
			err = clientErr
		}
	}))
	execPipeline.ServeHTTP(rw, channelReq)
	resp := rw.Result() //nolint:bodyclose

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

	diag.DefaultHTTPMonitoring.ClientRequestCompleted(ctx, channelReq.Method, req.Message().GetMethod(), strconv.Itoa(int(rsp.Status().GetCode())), contentLength, elapsedMs)

	return rsp, nil
}

func (h *Channel) constructRequest(ctx context.Context, req *invokev1.InvokeMethodRequest, appID string) (*http.Request, error) {
	// Construct app channel URI: VERB http://localhost:3000/method?query1=value1
	msg := req.Message()
	verb := msg.GetHttpExtension().GetVerb().String()
	method := msg.GetMethod()
	var headers []commonapi.NameValuePair

	uri := strings.Builder{}

	if appID != "" {
		// If appID includes http(s)://, use that as base URL
		// Otherwise, use baseAddress if this is an internal endpoint, or the base URL from the external endpoint configuration if external
		if strings.HasPrefix(appID, "https://") || strings.HasPrefix(appID, "http://") {
			uri.WriteString(appID)
		} else if endpoint, ok := h.compStore.GetHTTPEndpoint(appID); ok {
			uri.WriteString(endpoint.Spec.BaseURL)
			headers = endpoint.Spec.Headers
		} else {
			uri.WriteString(h.baseAddress)
		}
	} else {
		uri.WriteString(h.baseAddress)
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

	if ct := req.ContentType(); ct != "" {
		channelReq.Header.Set("content-type", ct)
	} else {
		channelReq.Header.Del("content-type")
	}

	// Configure headers from http endpoint CRD (if any)
	for _, hdr := range headers {
		channelReq.Header.Set(hdr.Name, hdr.Value.String())
	}

	if cl := channelReq.Header.Get(invokev1.ContentLengthHeader); cl != "" {
		v, err := strconv.ParseInt(cl, 10, 64)
		if err != nil {
			return nil, err
		}

		channelReq.ContentLength = v
	}

	// HTTP client needs to inject traceparent header for proper tracing stack.
	span := diagUtils.SpanFromContext(ctx)
	if span.SpanContext().HasTraceID() {
		tp := diag.SpanContextToW3CString(span.SpanContext())
		channelReq.Header.Set("traceparent", tp)
	}
	ts := diag.TraceStateToW3CString(span.SpanContext())
	if ts != "" {
		channelReq.Header.Set("tracestate", ts)
	}

	if h.appHeaderToken != "" {
		channelReq.Header.Set(securityConsts.APITokenHeader, h.appHeaderToken)
	}

	return channelReq, nil
}

func (h *Channel) parseChannelResponse(req *invokev1.InvokeMethodRequest, channelResp *http.Response) (*invokev1.InvokeMethodResponse, error) {
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

func copyHeader(dst http.Header, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}
