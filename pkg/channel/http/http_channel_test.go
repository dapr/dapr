package http

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/valyala/fasthttp"

	"github.com/actionscore/actions/pkg/channel"
	"github.com/stretchr/testify/assert"
)

type testHandler struct {
}

func (t *testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, r.URL.RawQuery)
}

type testHandlerHeaders struct {
}

func (t *testHandlerHeaders) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers := []string{}
	for k, v := range r.Header {
		headers = append(headers, fmt.Sprintf("%s=%s", k, v[0]))
	}
	io.WriteString(w, strings.Join(headers, ","))
}

func TestInvokeMethod(t *testing.T) {
	server := httptest.NewServer(&testHandler{})
	c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
	request := &channel.InvokeRequest{
		Metadata: map[string]string{QueryString: "param1=val1&param2=val2"},
	}
	response, err := c.InvokeMethod(request)
	assert.NoError(t, err)
	assert.Equal(t, "param1=val1&param2=val2", string(response.Data))
	server.Close()
}

func TestInvokeWithHeaders(t *testing.T) {
	server := httptest.NewServer(&testHandlerHeaders{})
	c := Channel{baseAddress: server.URL, client: &fasthttp.Client{}}
	request := &channel.InvokeRequest{
		Metadata: map[string]string{
			"headers": "h1=v1,h2=v2",
		},
	}
	response, err := c.InvokeMethod(request)
	assert.NoError(t, err)
	assert.Contains(t, string(response.Data), "H1=v1")
	assert.Contains(t, string(response.Data), "H2=v2")
	server.Close()
}
