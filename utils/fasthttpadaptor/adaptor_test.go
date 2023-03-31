package fasthttpadaptor

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

func TestNewFastHTTPHandler(t *testing.T) {
	t.Parallel()

	expectedMethod := fasthttp.MethodPost
	expectedProto := "HTTP/1.1"
	expectedProtoMajor := 1
	expectedProtoMinor := 1
	expectedRequestURI := "/foo/bar?baz=123"
	expectedBody := "<!doctype html><html>"
	expectedContentLength := len(expectedBody)
	expectedHost := "foobar.com"
	expectedRemoteAddr := "1.2.3.4:6789"
	expectedHeader := map[string][]string{
		"Foo-Bar":         {"baz"},
		"Abc":             {"defg"},
		"XXX-Remote-Addr": {"123.43.4543.345"},
		"Multi":           {"hello", "world"},
	}
	expectedURL, err := url.ParseRequestURI(expectedRequestURI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedContextKey := "contextKey"
	expectedContextValue := "contextValue"
	expectedContentType := "text/html; charset=utf-8"

	callsCount := 0
	nethttpH := func(w http.ResponseWriter, r *http.Request) {
		callsCount++
		if r.Method != expectedMethod {
			t.Fatalf("unexpected method %q. Expecting %q", r.Method, expectedMethod)
		}
		if r.Proto != expectedProto {
			t.Fatalf("unexpected proto %q. Expecting %q", r.Proto, expectedProto)
		}
		if r.ProtoMajor != expectedProtoMajor {
			t.Fatalf("unexpected protoMajor %d. Expecting %d", r.ProtoMajor, expectedProtoMajor)
		}
		if r.ProtoMinor != expectedProtoMinor {
			t.Fatalf("unexpected protoMinor %d. Expecting %d", r.ProtoMinor, expectedProtoMinor)
		}
		if r.RequestURI != expectedRequestURI {
			t.Fatalf("unexpected requestURI %q. Expecting %q", r.RequestURI, expectedRequestURI)
		}
		if r.ContentLength != int64(expectedContentLength) {
			t.Fatalf("unexpected contentLength %d. Expecting %d", r.ContentLength, expectedContentLength)
		}
		if len(r.TransferEncoding) != 0 {
			t.Fatalf("unexpected transferEncoding %q. Expecting []", r.TransferEncoding)
		}
		if r.Host != expectedHost {
			t.Fatalf("unexpected host %q. Expecting %q", r.Host, expectedHost)
		}
		if r.RemoteAddr != expectedRemoteAddr {
			t.Fatalf("unexpected remoteAddr %q. Expecting %q", r.RemoteAddr, expectedRemoteAddr)
		}
		body, rErr := io.ReadAll(r.Body)
		r.Body.Close()
		if rErr != nil {
			t.Fatalf("unexpected error when reading request body: %v", rErr)
		}
		if string(body) != expectedBody {
			t.Fatalf("unexpected body %q. Expecting %q", body, expectedBody)
		}
		if !reflect.DeepEqual(r.URL, expectedURL) {
			t.Fatalf("unexpected URL: %#v. Expecting %#v", r.URL, expectedURL)
		}
		if r.Context().Value(expectedContextKey) != expectedContextValue {
			t.Fatalf("unexpected context value for key %q. Expecting %q", expectedContextKey, expectedContextValue)
		}

		for k, expectedV := range expectedHeader {
			v := r.Header.Values(k)
			assert.Equalf(t, expectedV, v, "unexpected header values %q for key %q. Expecting %q", v, k, expectedV)
		}

		w.Header().Set("Header1", "value1")
		w.Header().Set("Header2", "value2")
		w.Header().Add("Multi", "value1")
		w.Header().Add("Multi", "value2")
		w.WriteHeader(http.StatusBadRequest)
		w.Write(body) //nolint:errcheck
	}
	fasthttpH := NewFastHTTPHandler(http.HandlerFunc(nethttpH))
	fasthttpH = setContextValueMiddleware(fasthttpH, expectedContextKey, expectedContextValue)

	var ctx fasthttp.RequestCtx
	var req fasthttp.Request

	req.Header.SetMethod(expectedMethod)
	req.SetRequestURI(expectedRequestURI)
	req.Header.SetHost(expectedHost)
	req.BodyWriter().Write([]byte(expectedBody)) //nolint:errcheck
	for k, v := range expectedHeader {
		for _, h := range v {
			req.Header.Add(k, h)
		}
	}

	remoteAddr, err := net.ResolveTCPAddr("tcp", expectedRemoteAddr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ctx.Init(&req, remoteAddr, nil)

	fasthttpH(&ctx)

	if callsCount != 1 {
		t.Fatalf("unexpected callsCount: %d. Expecting 1", callsCount)
	}

	resp := &ctx.Response
	if resp.StatusCode() != fasthttp.StatusBadRequest {
		t.Fatalf("unexpected statusCode: %d. Expecting %d", resp.StatusCode(), fasthttp.StatusBadRequest)
	}
	if string(resp.Header.Peek("Header1")) != "value1" {
		t.Fatalf("unexpected header value: %q. Expecting %q", resp.Header.Peek("Header1"), "value1")
	}
	if string(resp.Header.Peek("Header2")) != "value2" {
		t.Fatalf("unexpected header value: %q. Expecting %q", resp.Header.Peek("Header2"), "value2")
	}
	mh := resp.Header.PeekAll("Multi")
	assert.Equal(t, [][]byte{[]byte("value1"), []byte("value2")}, mh)
	if string(resp.Body()) != expectedBody {
		t.Fatalf("unexpected response body %q. Expecting %q", resp.Body(), expectedBody)
	}
	if string(resp.Header.Peek("Content-Type")) != expectedContentType {
		t.Fatalf("unexpected response content-type %q. Expecting %q", string(resp.Header.Peek("Content-Type")), expectedBody)
	}
}

func setContextValueMiddleware(next fasthttp.RequestHandler, key string, value interface{}) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		ctx.SetUserValue(key, value)
		next(ctx)
	}
}
