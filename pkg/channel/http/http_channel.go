package http

import (
	"crypto/tls"
	"fmt"
	"time"

	"github.com/actionscore/actions/pkg/channel"
	"github.com/valyala/fasthttp"
)

const (
	HTTPVerb       = "http.verb"
	HTTPStatusCode = "http.status_code"
	Get            = "GET"
	Post           = "POST"
	Delete         = "DELETE"
	Put            = "PUT"
	Options        = "OPTIONS"
)

type HTTPChannel struct {
	client      *fasthttp.Client
	baseAddress string
}

func (h *HTTPChannel) InvokeMethod(invokeRequest *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	req := fasthttp.AcquireRequest()
	uri := fmt.Sprintf("%s/%s", h.baseAddress, invokeRequest.Method)
	req.SetRequestURI(uri)
	req.SetBody(invokeRequest.Payload)
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

func CreateLocalChannel(port int) (channel.AppChannel, error) {
	return &HTTPChannel{
		client:      &fasthttp.Client{MaxConnsPerHost: 1000000, TLSConfig: &tls.Config{InsecureSkipVerify: true}, ReadTimeout: time.Second * 60},
		baseAddress: fmt.Sprintf("http://127.0.0.1:%v", port),
	}, nil
}
