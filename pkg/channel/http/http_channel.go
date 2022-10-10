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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
	streamutils "github.com/dapr/dapr/utils/streams"
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
	client              *http.Client
	baseAddress         string
	ch                  chan struct{}
	tracingSpec         config.TracingSpec
	appHeaderToken      string
	maxResponseBodySize int
	appHealthCheckPath  string
	appHealth           *apphealth.AppHealth
	pipeline            httpMiddleware.Pipeline
}

// CreateLocalChannel creates an HTTP AppChannel
//
//nolint:gosec
func CreateLocalChannel(port, maxConcurrency int, pipeline httpMiddleware.Pipeline, spec config.TracingSpec, sslEnabled bool, maxRequestBodySize, readBufferSize int) (channel.AppChannel, error) {
	scheme := httpScheme
	if sslEnabled {
		scheme = httpsScheme
	}

	c := &Channel{
		pipeline: pipeline,
		// We cannot use fasthttp here because of lack of streaming support
		client: &http.Client{
			Transport: &http.Transport{
				ReadBufferSize:         readBufferSize * 1024,
				MaxResponseHeaderBytes: int64(readBufferSize) * 1024,
			},
		},
		baseAddress:         fmt.Sprintf("%s://%s:%d", scheme, channel.DefaultChannelAddress, port),
		tracingSpec:         spec,
		appHeaderToken:      auth.GetAppToken(),
		maxResponseBodySize: maxRequestBodySize,
	}

	if sslEnabled {
		(c.client.Transport.(*http.Transport)).TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	if maxConcurrency > 0 {
		c.ch = make(chan struct{}, maxConcurrency)
	}

	return c, nil
}

// GetBaseAddress returns the application base address.
func (h *Channel) GetBaseAddress() string {
	return h.baseAddress
}

// GetAppConfig gets application config from user application
// GET http://localhost:<app_port>/dapr/config
func (h *Channel) GetAppConfig() (*config.ApplicationConfig, error) {
	req := invokev1.NewInvokeMethodRequest(appConfigEndpoint).
		WithHTTPExtension(http.MethodGet, "").
		WithRawData(nil, invokev1.JSONContentType)

	resp, err := h.InvokeMethod(context.TODO(), req)
	if err != nil {
		return nil, err
	}

	var config config.ApplicationConfig

	if resp.Status().Code != http.StatusOK {
		return &config, nil
	}

	// Get versioning info, currently only v1 is supported.
	headers := resp.Headers()
	var version string
	if val, ok := headers["dapr-app-config-version"]; ok {
		if len(val.Values) == 1 {
			version = val.Values[0]
		}
	}

	switch version {
	case "v1":
		fallthrough
	default:
		_, body := resp.RawData()
		if err = json.Unmarshal(body, &config); err != nil {
			return nil, err
		}
	}

	return &config, nil
}

// InvokeMethod invokes user code via HTTP.
func (h *Channel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	if h.appHealth != nil && h.appHealth.GetStatus() != apphealth.AppStatusHealthy {
		return nil, status.Error(codes.Internal, messages.ErrAppUnhealthy)
	}

	// Check if HTTP Extension is given. Otherwise, it will return error.
	httpExt := req.Message().GetHttpExtension()
	if httpExt == nil {
		return nil, status.Error(codes.InvalidArgument, "missing HTTP extension field")
	}
	// Go's net/http library does not support sending requests with the CONNECT method
	if httpExt.Verb == commonv1pb.HTTPExtension_NONE || httpExt.Verb == commonv1pb.HTTPExtension_CONNECT { //nolint:nosnakecase
		return nil, status.Error(codes.InvalidArgument, "invalid HTTP verb")
	}

	var rsp *invokev1.InvokeMethodResponse
	var err error
	switch req.APIVersion() {
	case internalv1pb.APIVersion_V1: //nolint:nosnakecase
		rsp, err = h.invokeMethodV1(ctx, req)

	default:
		// Reject unsupported version
		err = status.Error(codes.Unimplemented, fmt.Sprintf("Unsupported spec version: %d", req.APIVersion()))
	}

	return rsp, err
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

func (h *Channel) invokeMethodV1(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	channelReq, err := h.constructRequest(ctx, req)
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
		recorder := httptest.NewRecorder()
		// Reset the code which is 200 by default
		recorder.Code = 0
		execPipeline := h.pipeline.Apply(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			channelReq = r
		}))
		execPipeline.ServeHTTP(recorder, channelReq)

		// If there's a status code, it means that a middleware aborted the request
		// So, we get the response directly from the middlewares
		if recorder.Code > 0 {
			//nolint:bodyclose
			resp = recorder.Result()
		} else if len(recorder.Header()) > 0 {
			// Set headers in the outbound request
			for k, vs := range recorder.Header() {
				for _, v := range vs {
					channelReq.Header.Add(k, v)
				}
			}
		}
	}

	if resp == nil {
		// Send request to user application
		//nolint:bodyclose
		resp, err = h.client.Do(channelReq)
	}
	if resp != nil {
		defer resp.Body.Close()
	}

	elapsedMs := float64(time.Since(startRequest) / time.Millisecond)

	var contentLength int64
	if resp != nil && resp.Header != nil {
		contentLength, _ = strconv.ParseInt(resp.Header.Get("content-length"), 10, 64)
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

func (h *Channel) constructRequest(ctx context.Context, req *invokev1.InvokeMethodRequest) (*http.Request, error) {
	// Construct app channel URI: VERB http://localhost:3000/method?query1=value1
	var uri string
	verb := req.Message().HttpExtension.Verb.String()
	method := req.Message().Method
	if strings.HasPrefix(method, "/") {
		uri = h.baseAddress + method
	} else {
		uri = h.baseAddress + "/" + method
	}
	qs := req.EncodeHTTPQueryString()
	if qs != "" {
		uri += "?" + qs
	}

	ct, body := req.RawData()

	channelReq, err := http.NewRequestWithContext(ctx, verb, uri, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	// Recover headers
	invokev1.InternalMetadataToHTTPHeader(ctx, req.Metadata(), channelReq.Header.Set)
	channelReq.Header.Set("content-type", ct)

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

func (h *Channel) parseChannelResponse(req *invokev1.InvokeMethodRequest, channelResp *http.Response) (*invokev1.InvokeMethodResponse, error) {
	var contentType string

	contentType = channelResp.Header.Get("content-type")

	// Limit response body if needed
	var body io.ReadCloser
	if h.maxResponseBodySize > 0 {
		body = streamutils.LimitReadCloser(channelResp.Body, int64(h.maxResponseBodySize)*1024*1024)
	} else {
		body = channelResp.Body
	}

	bodyData, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	// Convert status code
	rsp := invokev1.
		NewInvokeMethodResponse(int32(channelResp.StatusCode), "", nil).
		WithHTTPHeaders(channelResp.Header).
		WithRawData(bodyData, contentType)

	return rsp, nil
}
