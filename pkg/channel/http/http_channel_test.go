package http

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/valyala/fasthttp"

	"github.com/actionscore/actions/pkg/channel"
	"github.com/stretchr/testify/assert"
)

func TestInvokeMethod(t *testing.T) {
	srv := &http.Server{Addr: ":5555"}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.URL.RawQuery)
	})
	go func() {
		srv.ListenAndServe()
	}()
	c := Channel{baseAddress: "http://localhost:5555", client: &fasthttp.Client{}}
	request := &channel.InvokeRequest{
		Metadata: map[string]string{QueryString: "param1=val1&param2=val2"},
	}
	response, err := c.InvokeMethod(request)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assert.NoError(t, err)
	assert.Equal(t, "param1=val1&param2=val2", string(response.Data))
	srv.Shutdown(ctx)
}
