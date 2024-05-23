package diagnostics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/config"
)

func BenchmarkHTTPMiddlewareLowCardinalityNoPathNormalization(b *testing.B) {
	requestBody := "fake_requestDaprBody"
	responseBody := "fake_responseDaprBody"

	// create test httpMetrics
	testHTTP := newHTTPMetrics()
	pathNormalization := &config.PathNormalization{
		Enabled: false,
	}
	testHTTP.Init("fakeID", pathNormalization, false)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act && assert
	for i := 0; i < b.N; i++ {
		fmt.Print()
		testRequest := fakeOrdersHTTPRequest(requestBody, i)
		handler.ServeHTTP(httptest.NewRecorder(), testRequest)
	}
}

func BenchmarkHTTPMiddlewareHighCardinalityNoPathNormalization(b *testing.B) {
	requestBody := "fake_requestDaprBody"
	responseBody := "fake_responseDaprBody"

	// create test httpMetrics
	testHTTP := newHTTPMetrics()
	pathNormalization := &config.PathNormalization{
		Enabled: false,
	}
	testHTTP.Init("fakeID", pathNormalization, true)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act && assert
	for i := 0; i < b.N; i++ {
		fmt.Print()
		testRequest := fakeOrdersHTTPRequest(requestBody, i)
		handler.ServeHTTP(httptest.NewRecorder(), testRequest)
	}
}

func BenchmarkHTTPMiddlewareLowCardinalityWithPathNormalization(b *testing.B) {
	requestBody := "fake_requestDaprBody"
	responseBody := "fake_responseDaprBody"

	// create test httpMetrics
	testHTTP := newHTTPMetrics()
	pathNormalization := &config.PathNormalization{
		Enabled: true,
		IngressPaths: []string{
			"/invoke/method/orders/{orderID}",
		},
		EgressPaths: []string{
			"/invoke/method/orders/{orderID}",
		},
	}
	testHTTP.Init("fakeID", pathNormalization, false)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act && assert
	for i := 0; i < b.N; i++ {
		fmt.Print()
		testRequest := fakeOrdersHTTPRequest(requestBody, i)
		handler.ServeHTTP(httptest.NewRecorder(), testRequest)
	}
}

func BenchmarkHTTPMiddlewareHighCardinalityWithPathNormalization(b *testing.B) {
	requestBody := "fake_requestDaprBody"
	responseBody := "fake_responseDaprBody"

	// create test httpMetrics
	testHTTP := newHTTPMetrics()
	pathNormalization := &config.PathNormalization{
		Enabled: true,
		IngressPaths: []string{
			"/invoke/method/orders/{orderID}",
		},
		EgressPaths: []string{
			"/invoke/method/orders/{orderID}",
		},
	}
	testHTTP.Init("fakeID", pathNormalization, true)

	handler := testHTTP.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		w.Write([]byte(responseBody))
	}))

	// act && assert
	for i := 0; i < b.N; i++ {
		fmt.Print()
		testRequest := fakeOrdersHTTPRequest(requestBody, i)
		handler.ServeHTTP(httptest.NewRecorder(), testRequest)
	}
}

func fakeOrdersHTTPRequest(body string, id int) *http.Request {
	url := fmt.Sprintf("http://dapr.io/invoke/method/orders/%d", id)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Correlation-ID", "e6f4bb20-96c0-426a-9e3d-991ba16a3ebb")
	req.Header.Set("XXX-Remote-Addr", "192.168.0.100")
	req.Header.Set("Transfer-Encoding", "encoding")
	// This is normally set automatically when the request is sent to a server, but in this case we are not using a real server
	req.Header.Set("Content-Length", strconv.FormatInt(req.ContentLength, 10))

	return req
}
