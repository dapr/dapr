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
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	nethttp "net/http"

	"github.com/valyala/fasthttp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/apphealth"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diagUtils "github.com/dapr/dapr/pkg/diagnostics/utils"
	"github.com/dapr/dapr/pkg/messages"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	authConsts "github.com/dapr/dapr/pkg/runtime/security/consts"
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
	client              *fasthttp.Client
	baseAddress         string
	ch                  chan struct{}
	tracingSpec         config.TracingSpec
	appHeaderToken      string
	maxResponseBodySize int
	appHealthCheckPath  string
	appHealth           *apphealth.AppHealth
}

// CreateLocalChannel creates an HTTP AppChannel
//
//nolint:gosec
func CreateLocalChannel(port, maxConcurrency int, spec config.TracingSpec, sslEnabled bool, maxRequestBodySize int, readBufferSize int) (channel.AppChannel, error) {
	scheme := httpScheme
	if sslEnabled {
		scheme = httpsScheme
	}

	c := &Channel{
		client: &fasthttp.Client{
			MaxConnsPerHost:           1000000,
			MaxIdemponentCallAttempts: 0,
			MaxResponseBodySize:       maxRequestBodySize * 1024 * 1024,
			ReadBufferSize:            readBufferSize * 1024,
			DisablePathNormalizing:    true,
		},
		baseAddress:         fmt.Sprintf("%s://%s:%d", scheme, channel.DefaultChannelAddress, port),
		tracingSpec:         spec,
		appHeaderToken:      auth.GetAppToken(),
		maxResponseBodySize: maxRequestBodySize,
	}

	if sslEnabled {
		c.client.TLSConfig = &tls.Config{InsecureSkipVerify: true}
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
	req := invokev1.NewInvokeMethodRequest(appConfigEndpoint)
	req.WithHTTPExtension(nethttp.MethodGet, "")
	req.WithRawData(nil, invokev1.JSONContentType)

	// TODO Propagate context
	ctx := context.Background()
	resp, err := h.InvokeMethod(ctx, req)
	if err != nil {
		return nil, err
	}

	var config config.ApplicationConfig

	if resp.Status().Code != nethttp.StatusOK {
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
	if httpExt.GetVerb() == commonv1pb.HTTPExtension_NONE { //nolint:nosnakecase
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
	channelReq := fasthttp.AcquireRequest()
	channelResp := fasthttp.AcquireResponse()

	defer func() {
		fasthttp.ReleaseRequest(channelReq)
		fasthttp.ReleaseResponse(channelResp)
	}()

	channelReq.URI().Update(h.baseAddress + h.appHealthCheckPath)
	channelReq.URI().DisablePathNormalizing = true
	channelReq.Header.SetMethod(fasthttp.MethodGet)

	diag.DefaultHTTPMonitoring.AppHealthProbeStarted(ctx)
	startRequest := time.Now()

	err := h.client.Do(channelReq, channelResp)

	elapsedMs := float64(time.Since(startRequest) / time.Millisecond)

	if err != nil {
		// Errors here are network-level errors, so we are not returning them as errors
		// Instead, we just return a failed probe
		diag.DefaultHTTPMonitoring.AppHealthProbeCompleted(ctx, strconv.Itoa(nethttp.StatusInternalServerError), elapsedMs)
		//nolint:nilerr
		return false, nil
	}

	code := channelResp.StatusCode()
	status := code >= 200 && code < 300
	diag.DefaultHTTPMonitoring.AppHealthProbeCompleted(ctx, strconv.Itoa(code), elapsedMs)

	return status, nil
}

func (h *Channel) invokeMethodV1(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	channelReq := h.constructRequest(ctx, req)

	if h.ch != nil {
		h.ch <- struct{}{}
	}
	defer func() {
		if h.ch != nil {
			<-h.ch
		}
	}()

	// Emit metric when request is sent
	verb := string(channelReq.Header.Method())
	diag.DefaultHTTPMonitoring.ClientRequestStarted(ctx, verb, req.Message().Method, int64(len(req.Message().Data.GetValue())))
	startRequest := time.Now()

	// Send request to user application
	resp := fasthttp.AcquireResponse()

	err := h.client.Do(channelReq, resp)
	defer func() {
		fasthttp.ReleaseRequest(channelReq)
		fasthttp.ReleaseResponse(resp)
	}()

	elapsedMs := float64(time.Since(startRequest) / time.Millisecond)

	if err != nil {
		diag.DefaultHTTPMonitoring.ClientRequestCompleted(ctx, verb, req.Message().GetMethod(), strconv.Itoa(nethttp.StatusInternalServerError), int64(resp.Header.ContentLength()), elapsedMs)
		return nil, err
	}

	rsp := h.parseChannelResponse(req, resp)
	diag.DefaultHTTPMonitoring.ClientRequestCompleted(ctx, verb, req.Message().GetMethod(), strconv.Itoa(int(rsp.Status().Code)), int64(resp.Header.ContentLength()), elapsedMs)

	return rsp, nil
}

func (h *Channel) constructRequest(ctx context.Context, req *invokev1.InvokeMethodRequest) *fasthttp.Request {
	channelReq := fasthttp.AcquireRequest()

	// Construct app channel URI: VERB http://localhost:3000/method?query1=value1
	var uri string
	method := req.Message().GetMethod()
	if strings.HasPrefix(method, "/") {
		uri = fmt.Sprintf("%s%s", h.baseAddress, method)
	} else {
		uri = fmt.Sprintf("%s/%s", h.baseAddress, method)
	}
	channelReq.URI().Update(uri)
	channelReq.URI().DisablePathNormalizing = true
	channelReq.URI().SetQueryString(req.EncodeHTTPQueryString())
	channelReq.Header.SetMethod(req.Message().HttpExtension.Verb.String())

	// Recover headers
	invokev1.InternalMetadataToHTTPHeader(ctx, req.Metadata(), channelReq.Header.Set)

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

	// Set Content body and types
	contentType, body := req.RawData()
	channelReq.Header.SetContentType(contentType)
	channelReq.SetBody(body)

	return channelReq
}

func (h *Channel) parseChannelResponse(req *invokev1.InvokeMethodRequest, resp *fasthttp.Response) *invokev1.InvokeMethodResponse {
	var statusCode int
	var contentType string
	var body []byte

	statusCode = resp.StatusCode()

	// TODO: Remove entire block when feature is finalized
	if config.GetNoDefaultContentType() {
		resp.Header.SetNoDefaultContentType(true)
	}
	contentType = (string)(resp.Header.ContentType())
	body = resp.Body()

	// Convert status code
	rsp := invokev1.NewInvokeMethodResponse(int32(statusCode), "", nil)
	rsp.WithFastHTTPHeaders(&resp.Header).WithRawData(body, contentType)

	return rsp
}
