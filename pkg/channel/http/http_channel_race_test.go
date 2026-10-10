/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

// isolationApp drops the connection for paths containing "fail" and echoes
// the path otherwise.
func isolationApp(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond)
		if strings.Contains(r.URL.Path, "fail") {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				conn.Close()
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, r.URL.Path)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newIsolationChannel(t *testing.T, pipelines ...*config.PipelineSpec) *Channel {
	t.Helper()

	var spec *config.PipelineSpec
	if len(pipelines) > 0 {
		spec = pipelines[0]
	}
	return &Channel{
		baseAddress: isolationApp(t).URL,
		client:      &http.Client{Transport: &http.Transport{DisableKeepAlives: true}},
		compStore:   compstore.New(),
		tracingSpec: &config.TracingSpec{SamplingRate: "0"},
		middleware:  httpMiddleware.New().BuildPipelineFromSpec("app", spec),
	}
}

type outcome struct {
	name string
	fail bool
	err  error
	body string
}

func runConcurrent(t *testing.T, n int, do func(name string) (*invokev1.InvokeMethodResponse, error)) {
	t.Helper()

	outcomes := make([]outcome, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			o := outcome{name: "ok" + strconv.Itoa(i), fail: i%2 == 0}
			if o.fail {
				o.name = "fail" + strconv.Itoa(i)
			}
			resp, err := do(o.name)
			o.err = err
			if resp != nil {
				if err == nil {
					b, rerr := resp.RawDataFull()
					o.body, o.err = string(b), rerr
				}
				resp.Close()
			}
			outcomes[i] = o
		})
	}
	wg.Wait()

	for _, o := range outcomes {
		if o.fail {
			require.Error(t, o.err, "%s must fail", o.name)
			continue
		}
		require.NoError(t, o.err, "%s must succeed", o.name)
		require.Contains(t, o.body, o.name, "%s got another request's response", o.name)
	}
}

func TestInvokeMethod_concurrentRequestsIsolated(t *testing.T) {
	c := newIsolationChannel(t)

	for range 20 {
		runConcurrent(t, 16, func(name string) (*invokev1.InvokeMethodResponse, error) {
			req := invokev1.NewInvokeMethodRequest(name).WithHTTPExtension(http.MethodGet, "")
			defer req.Close()
			return c.InvokeMethod(t.Context(), req, "")
		})
	}
}

func TestTriggerJob_concurrentRequestsIsolated(t *testing.T) {
	c := newIsolationChannel(t)

	data, err := anypb.New(wrapperspb.String("x"))
	require.NoError(t, err)

	for range 20 {
		runConcurrent(t, 16, func(name string) (*invokev1.InvokeMethodResponse, error) {
			return c.TriggerJob(t.Context(), name, name, data)
		})
	}
}

func TestInvokeMethod_concurrentRequestsIsolatedWithMiddleware(t *testing.T) {
	pipeline := httpMiddleware.New()
	pipeline.Add(httpMiddleware.Spec{
		Component: compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "passthrough"},
			Spec:       compapi.ComponentSpec{Type: "middleware.http.passthrough", Version: "v1"},
		},
		Implementation: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(r.Context()))
			})
		},
	})

	c := newIsolationChannel(t)
	c.middleware = pipeline.BuildPipelineFromSpec("app", &config.PipelineSpec{
		Handlers: []config.HandlerSpec{{Name: "passthrough", Type: "middleware.http.passthrough", Version: "v1"}},
	})

	for range 5 {
		runConcurrent(t, 32, func(name string) (*invokev1.InvokeMethodResponse, error) {
			req := invokev1.NewInvokeMethodRequest(name).WithHTTPExtension(http.MethodGet, "")
			defer req.Close()
			return c.InvokeMethod(t.Context(), req, "")
		})
	}
}
