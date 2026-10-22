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
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/api/universal"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/kit/logger"

	v1alpha1 "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestOnDirectMessageJoinsStreamingGoroutineAfterTimeout is a regression test
// for dapr#10371: when a resiliency timeout fires while the policy operation
// goroutine is still streaming the app's response body into the
// http.ResponseWriter (io.Copy inside the operation), the HTTP handler must
// not return until that streaming has finished. Before the fix,
// onDirectMessage returned as soon as the runner returned ctx.Err(), leaving
// the goroutine writing into `w` after the handler returned — which panics
// once the net/http framework reclaims the writer.
func TestOnDirectMessageJoinsStreamingGoroutineAfterTimeout(t *testing.T) {
	// drained is set when the slow response body has been fully read by the
	// handler-side io.Copy. If the handler returns before this, the streaming
	// was still in flight after the handler returned - the bug.
	var drained atomic.Bool

	slow := &slowReader{
		chunkEvery: 20 * time.Millisecond,
		chunks:     25, // ~500ms total, far beyond the 100ms timeout
		onEOF:      func() { drained.Store(true) },
	}

	dm := &slowResponseDirectMessaging{body: slow}

	res := &v1alpha1.Resiliency{
		ObjectMeta: metav1.ObjectMeta{Name: "res"},
		Spec: v1alpha1.ResiliencySpec{
			Policies: v1alpha1.Policies{
				Timeouts: map[string]string{"fast": "100ms"},
			},
			Targets: v1alpha1.Targets{
				Apps: map[string]v1alpha1.EndpointPolicyNames{
					"streamApp": {Timeout: "fast"},
				},
			},
		},
	}

	compStore := compstore.New()
	testAPI := &api{
		directMessaging: dm,
		universal: universal.New(universal.Options{
			CompStore:  compStore,
			Resiliency: resiliency.FromConfigurations(logger.NewLogger("messaging.test"), res),
		}),
	}

	fakeServer := newFakeHTTPServer()
	fakeServer.StartServer(testAPI.constructDirectMessagingEndpoints(), nil)

	fakeServer.DoRequest("POST", "v1.0/invoke/streamApp/method/stream", []byte("x"), nil)
	fakeServer.Shutdown()

	if !drained.Load() {
		t.Fatal("HTTP handler returned while the response body was still being streamed into the ResponseWriter (dapr#10371 regression)")
	}
}

// slowResponseDirectMessaging returns a 200 response whose body streams slowly,
// mirroring an app that produces a chunked/streamed response.
type slowResponseDirectMessaging struct {
	body *slowReader
}

func (d *slowResponseDirectMessaging) Invoke(ctx context.Context, targetAppID string, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	resp := invokev1.NewInvokeMethodResponse(int32(http.StatusOK), "OK", nil)
	resp.WithRawData(d.body) // the handler-side io.Copy drains this
	return resp, nil
}

// slowReader emits a chunk every chunkEvery until chunks are exhausted,
// ignoring context cancellation — like a socket feed that keeps producing
// regardless of the caller going away. This is what kept the operation
// goroutine busy past the resiliency timeout in dapr#10371.
type slowReader struct {
	chunkEvery time.Duration
	chunks     int
	onEOF      func()
	i          int
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.i >= s.chunks {
		if s.onEOF != nil {
			s.onEOF()
		}
		return 0, io.EOF
	}
	time.Sleep(s.chunkEvery)
	n := copy(p, "chunk")
	s.i++
	return n, nil
}
