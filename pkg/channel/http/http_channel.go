package http

import (
	"crypto/tls"
	"fmt"
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
	fasthttp.ReleaseRequest(req)
	fasthttp.ReleaseResponse(resp)

	return &channel.InvokeResponse{
		Metadata: map[string]string{HTTPStatusCode: fmt.Sprintf("%v", statusCode)},
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
