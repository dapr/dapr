// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/config"
	tracing "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/valyala/fasthttp"
	"go.opencensus.io/trace"
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
			headers := strings.Split(val, "&__header_delim__&")
			for _, h := range headers {
				kv := strings.Split(h, "&__header_equals__&")
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
		if corID, ok := invokeRequest.Metadata[tracing.CorrelationID]; ok {
			req.Header.Set(tracing.CorrelationID, corID)
		}
	}

	span, spanc := tracing.TraceSpanFromFastHTTPRequest(req, h.tracingSpec)
	defer span.Span.End()
	defer spanc.Span.End()

	req.Header.Set(tracing.CorrelationID, tracing.SerializeSpanContext(*spanc.SpanContext))

	resp := fasthttp.AcquireResponse()

	if h.ch != nil {
		h.ch <- 1
	}

	err := h.client.Do(req, resp)
	if h.ch != nil {
		<-h.ch
	}
	if err != nil {
		return nil, err
	}

	body := resp.Body()
	arr := make([]byte, len(body))
	copy(arr, body)

	statusCode := resp.StatusCode()
	headers := []string{}
	resp.Header.VisitAll(func(key []byte, value []byte) {
		headers = append(headers, fmt.Sprintf("%s&__header_equals__&%s", string(key), string(value)))
	})

	metadata := map[string]string{HTTPStatusCode: fmt.Sprintf("%v", statusCode)}
	if len(headers) > 0 {
		metadata["headers"] = strings.Join(headers, "&__header_delim__&")
	}

	spanc.Span.SetStatus(trace.Status{
		Code:    tracing.ProjectStatusCode(resp.StatusCode()),
		Message: strconv.Itoa(resp.StatusCode()),
	})

	span.Span.SetStatus(trace.Status{
		Code:    tracing.ProjectStatusCode(resp.StatusCode()),
		Message: strconv.Itoa(resp.StatusCode()),
	})

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
