// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/valyala/fasthttp"
)

const (
	// HTTPVerb is an dapr http channel verb
	HTTPVerb = "http.verb"
	// HTTPStatusCode is an dapr http channel status code
	HTTPStatusCode = "http.status_code"
	// Get is an HTTP Get method
	Get = "GET"
	// Post is an Post Get method
	Post = "POST"
	// Delete is an HTTP Delete method
	Delete = "DELETE"
	// Put is an HTTP Put method
	Put = "PUT"
	// Options is an HTTP OPTIONS method
	Options = "OPTIONS"
	// QueryString is the query string passed by the request
	QueryString = "http.query_string"
	// ContentType is the header for Content-Type
	ContentType        = "Content-Type"
	defaultContentType = "application/json"

	httpInternalErrorCode = "500"
	HeaderDelim           = "&__header_delim__&"
	HeaderEquals          = "&__header_equals__&"
)

// Channel is an HTTP implementation of an AppChannel
type Channel struct {
	client      *fasthttp.Client
	baseAddress string
	ch          chan int
	tracingSpec config.TracingSpec
}

func applyContentTypeIfNotPresent(req *fasthttp.Request) {
	if len(req.Header.ContentType()) == 0 {
		req.Header.SetContentType(defaultContentType)
	}
}

// GetBaseAddress returns the application base address
func (h *Channel) GetBaseAddress() string {
	return h.baseAddress
}

// InvokeMethod invokes user code via HTTP
func (h *Channel) InvokeMethod(invokeRequest *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	req := fasthttp.AcquireRequest()
	uri := fmt.Sprintf("%s/%s", h.baseAddress, invokeRequest.Method)
	req.SetRequestURI(uri)
	req.SetBody(invokeRequest.Payload)
	if val, ok := invokeRequest.Metadata[QueryString]; ok {
		req.URI().SetQueryString(val)
	}
	if invokeRequest.Metadata != nil {
		if val, ok := invokeRequest.Metadata["headers"]; ok {
			headers := strings.Split(val, HeaderDelim)
			for _, h := range headers {
				kv := strings.Split(h, HeaderEquals)
				req.Header.Set(kv[0], kv[1])
			}
		}
	}
	applyContentTypeIfNotPresent(req)

	method := invokeRequest.Metadata[HTTPVerb]
	if method == "" {
		method = Post
	}
	req.Header.SetMethod(method)

	if invokeRequest.Metadata != nil {
		if corID, ok := invokeRequest.Metadata[diag.CorrelationID]; ok {
			req.Header.Set(diag.CorrelationID, corID)
		}
	}

	var span diag.TracerSpan
	var spanc diag.TracerSpan

	span, spanc = diag.TraceSpanFromFastHTTPRequest(req, h.tracingSpec)
	defer span.Span.End()
	defer spanc.Span.End()

	req.Header.Set(diag.CorrelationID, diag.SerializeSpanContext(*spanc.SpanContext))

	resp := fasthttp.AcquireResponse()

	if h.ch != nil {
		h.ch <- 1
	}

	// TODO: Use propagated context
	ctx := context.Background()
	// Emit metric when request is sent
	diag.DefaultHTTPMonitoring.ClientRequestStarted(
		ctx, method, invokeRequest.Method,
		int64(len(invokeRequest.Payload)))

	startRequest := time.Now()
	err := h.client.Do(req, resp)
	elapsedMs := float64(time.Since(startRequest) / time.Millisecond)

	if h.ch != nil {
		<-h.ch
	}
	if err != nil {
		// Track failure request
		diag.DefaultHTTPMonitoring.ClientRequestCompleted(
			ctx, method, invokeRequest.Method,
			httpInternalErrorCode, 0, elapsedMs)
		return nil, err
	}

	body := resp.Body()
	arr := make([]byte, len(body))
	copy(arr, body)

	statusCode := resp.StatusCode()

	// Emit metric when requst is completed
	diag.DefaultHTTPMonitoring.ClientRequestCompleted(
		ctx, method, invokeRequest.Method,
		strconv.Itoa(statusCode), int64(len(body)), elapsedMs)

	headers := []string{}
	resp.Header.VisitAll(func(key []byte, value []byte) {
		headers = append(headers, fmt.Sprintf("%s&__header_equals__&%s", string(key), string(value)))
	})

	metadata := map[string]string{HTTPStatusCode: fmt.Sprintf("%v", statusCode)}
	if len(headers) > 0 {
		metadata["headers"] = strings.Join(headers, "&__header_delim__&")
	}

	diag.UpdateSpanPairStatusesFromHTTPResponse(span, spanc, resp)

	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(resp)

	return &channel.InvokeResponse{
		Metadata: metadata,
		Data:     arr,
	}, nil
}

// CreateLocalChannel creates an HTTP AppChannel
// nolint:gosec
func CreateLocalChannel(port, maxConcurrency int, spec config.TracingSpec) (channel.AppChannel, error) {
	c := &Channel{
		client:      &fasthttp.Client{MaxConnsPerHost: 1000000, TLSConfig: &tls.Config{InsecureSkipVerify: true}, ReadTimeout: time.Second * 60, MaxIdemponentCallAttempts: 0},
		baseAddress: fmt.Sprintf("http://127.0.0.1:%v", port),
		tracingSpec: spec,
	}
	if maxConcurrency > 0 {
		c.ch = make(chan int, maxConcurrency)
	}
	return c, nil
}
