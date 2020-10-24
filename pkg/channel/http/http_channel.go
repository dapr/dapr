// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"strconv"
	"time"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	diag_utils "github.com/dapr/dapr/pkg/diagnostics/utils"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	auth "github.com/dapr/dapr/pkg/runtime/security"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/plugin/ochttp/propagation/tracecontext"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// HTTPStatusCode is an dapr http channel status code
	HTTPStatusCode = "http.status_code"
	httpScheme     = "http"
	httpsScheme    = "https"
)

// Channel is an HTTP implementation of an AppChannel
type Channel struct {
	client         *fasthttp.Client
	baseAddress    string
	ch             chan int
	tracingSpec    config.TracingSpec
	appHeaderToken string
}

// CreateLocalChannel creates an HTTP AppChannel
// nolint:gosec
func CreateLocalChannel(port, maxConcurrency int, timeout time.Duration, spec config.TracingSpec, sslEnabled bool) (channel.AppChannel, error) {
	scheme := httpScheme
	if sslEnabled {
		scheme = httpsScheme
	}

	c := &Channel{
		client: &fasthttp.Client{
			MaxConnsPerHost:           1000000,
			MaxIdemponentCallAttempts: 0,
			ReadTimeout:               timeout,
		},
		baseAddress:    fmt.Sprintf("%s://%s:%d", scheme, channel.DefaultChannelAddress, port),
		tracingSpec:    spec,
		appHeaderToken: auth.GetAppToken(),
	}

	if sslEnabled {
		c.client.TLSConfig = &tls.Config{InsecureSkipVerify: true}
	}

	if maxConcurrency > 0 {
		c.ch = make(chan int, maxConcurrency)
	}
	return c, nil
}

// GetBaseAddress returns the application base address
func (h *Channel) GetBaseAddress() string {
	return h.baseAddress
}

// InvokeMethod invokes user code via HTTP
func (h *Channel) InvokeMethod(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	// Check if HTTP Extension is given. Otherwise, it will return error.
	httpExt := req.Message().GetHttpExtension()
	if httpExt == nil {
		return nil, status.Error(codes.InvalidArgument, "missing HTTP extension field")
	}
	if httpExt.GetVerb() == commonv1pb.HTTPExtension_NONE {
		return nil, status.Error(codes.InvalidArgument, "invalid HTTP verb")
	}

	var rsp *invokev1.InvokeMethodResponse
	var err error
	switch req.APIVersion() {
	case internalv1pb.APIVersion_V1:
		rsp, err = h.invokeMethodV1(ctx, req)

	default:
		// Reject unsupported version
		err = status.Error(codes.Unimplemented, fmt.Sprintf("Unsupported spec version: %d", req.APIVersion()))
	}

	return rsp, err
}

func (h *Channel) invokeMethodV1(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	channelReq := h.constructRequest(ctx, req)

	if h.ch != nil {
		h.ch <- 1
	}

	// Emit metric when request is sent
	verb := string(channelReq.Header.Method())
	diag.DefaultHTTPMonitoring.ClientRequestStarted(ctx, verb, req.Message().Method, int64(len(req.Message().Data.GetValue())))
	startRequest := time.Now()

	// Send request to user application
	var resp = fasthttp.AcquireResponse()
	err := h.client.DoTimeout(channelReq, resp, h.client.ReadTimeout)
	defer func() {
		fasthttp.ReleaseRequest(channelReq)
		fasthttp.ReleaseResponse(resp)
	}()

	elapsedMs := float64(time.Since(startRequest) / time.Millisecond)

	if h.ch != nil {
		<-h.ch
	}

	rsp := h.parseChannelResponse(req, resp, err)
	diag.DefaultHTTPMonitoring.ClientRequestCompleted(ctx, verb, req.Message().GetMethod(), strconv.Itoa(int(rsp.Status().Code)), int64(resp.Header.ContentLength()), elapsedMs)

	return rsp, nil
}

func (h *Channel) constructRequest(ctx context.Context, req *invokev1.InvokeMethodRequest) *fasthttp.Request {
	var channelReq = fasthttp.AcquireRequest()

	// Construct app channel URI: VERB http://localhost:3000/method?query1=value1
	uri := fmt.Sprintf("%s/%s", h.baseAddress, req.Message().GetMethod())
	channelReq.SetRequestURI(uri)
	channelReq.URI().SetQueryString(req.EncodeHTTPQueryString())
	channelReq.Header.SetMethod(req.Message().HttpExtension.Verb.String())

	// Recover headers
	invokev1.InternalMetadataToHTTPHeader(ctx, req.Metadata(), channelReq.Header.Set)

	// HTTP client needs to inject traceparent header for proper tracing stack.
	span := diag_utils.SpanFromContext(ctx)
	httpFormat := &tracecontext.HTTPFormat{}
	tp, ts := httpFormat.SpanContextToHeaders(span.SpanContext())
	channelReq.Header.Set("traceparent", tp)
	if ts != "" {
		channelReq.Header.Set("tracestate", ts)
	}

	if h.appHeaderToken != "" {
		channelReq.Header.Set(auth.APITokenHeader, h.appHeaderToken)
	}

	// Set Content body and types
	contentType, body := req.RawData()
	channelReq.Header.SetContentType(contentType)
	channelReq.SetBody(body)

	return channelReq
}

func (h *Channel) parseChannelResponse(req *invokev1.InvokeMethodRequest, resp *fasthttp.Response, respErr error) *invokev1.InvokeMethodResponse {
	var statusCode int
	var contentType string
	var body []byte

	if respErr != nil {
		statusCode = fasthttp.StatusInternalServerError
		contentType = string(invokev1.JSONContentType)
		body = []byte(fmt.Sprintf("{\"error\": \"client error: %s\"}", respErr))
	} else {
		statusCode = resp.StatusCode()
		contentType = (string)(resp.Header.ContentType())
		body = resp.Body()
	}

	// Convert status code
	rsp := invokev1.NewInvokeMethodResponse(int32(statusCode), "", nil)
	rsp.WithFastHTTPHeaders(&resp.Header).WithRawData(body, contentType)

	return rsp
}
