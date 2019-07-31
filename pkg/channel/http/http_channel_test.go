package http

import (
	"io"
	"net/http"
	"net/http/httptest"
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
