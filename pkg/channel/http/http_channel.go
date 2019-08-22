package http

import (
	"crypto/tls"
	"fmt"
	"strings"
	"time"

	"github.com/actionscore/actions/pkg/channel"
	"github.com/valyala/fasthttp"
)

const (
	// HTTPVerb is an actions http channel verb
	HTTPVerb = "http.verb"
	// HTTPStatusCode is an actions http channel status code
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
)

// Channel is an HTTP implementation of an AppChannel
type Channel struct {
	client      *fasthttp.Client
	baseAddress string
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
	req.Header.SetContentType("application/json")

	method := invokeRequest.Metadata[HTTPVerb]
	if method == "" {
		method = Post
	}
	req.Header.SetMethod(method)

	resp := fasthttp.AcquireResponse()
	err := h.client.Do(req, resp)
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
	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(resp)

	return &channel.InvokeResponse{
		Metadata: metadata,
		Data:     arr,
	}, nil
}

// CreateLocalChannel creates an HTTP AppChannel
func CreateLocalChannel(port int) (channel.AppChannel, error) {
	return &Channel{
		client:      &fasthttp.Client{MaxConnsPerHost: 1000000, TLSConfig: &tls.Config{InsecureSkipVerify: true}, ReadTimeout: time.Second * 60},
		baseAddress: fmt.Sprintf("http://127.0.0.1:%v", port),
	}, nil
}
