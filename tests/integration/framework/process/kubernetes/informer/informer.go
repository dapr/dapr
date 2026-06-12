/*
Copyright 2023 The Dapr Authors
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

package informer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/dapr/kit/events/loop"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	wfaclapi "github.com/dapr/dapr/pkg/apis/workflowaccesspolicy/v1alpha1"
)

// informedSendTimeout is how long inform() waits when notifying
// test-side observers (e.g. DeleteWait) before failing the test.
const informedSendTimeout = 3 * time.Second

// rotateInterval is how long a /watch connection is held open before the
// fake informer closes it to force the K8s reflector on the other end to
// reconnect and re-LIST. See the comment on the close goroutine inside
// Handler for why this exists.
const rotateInterval = 200 * time.Millisecond

// streamFactory pools queue segments across all active /watch handlers.
// The segment size is small because most watchers process events as fast
// as they arrive; the loop grows additional segments on demand when a
// burst exceeds one segment.
var streamFactory = loop.New[streamEvent](16)

// streamEvent is the event type for the per-watcher event loop. A non-nil
// payload is a watch event to deliver; closed=true is the shutdown
// sentinel emitted on disconnect so the loop drains and exits.
type streamEvent struct {
	payload []byte
	closed  bool
}

// Informer is a fake informer that pushes events to Kubernetes watch
// clients over a single long-lived chunked HTTP response.
//
// Previously each call to /watch popped one queued event and closed the
// connection, forcing the K8s reflector inside the operator to reconnect
// for every event with exponential backoff. That made every event cost
// seconds of test wall time. Now the handler holds the connection open
// for the lifetime of the request and streams events out as they arrive,
// so the reflector observes a steady-state watch and never needs to
// backoff between events. The per-watcher queue is a dapr/kit/events/loop
// so we never have to bound the in-flight buffer.
type Informer struct {
	lock sync.Mutex

	// backlog is per-GVK events emitted while no watcher was connected
	// for that GVK. Drained on the next /watch connection.
	backlog map[string][][]byte

	// streams is per-GVK loops held by currently-active /watch handlers.
	// inform() fans events out to every loop.
	streams map[string][]loop.Interface[streamEvent]

	// informed is test-side observation channels (DeleteWait & co.).
	// Fired when an event is actually delivered to a watcher.
	informed map[uint64]chan *metav1.WatchEvent
}

func New() *Informer {
	return &Informer{
		backlog:  make(map[string][][]byte),
		streams:  make(map[string][]loop.Interface[streamEvent]),
		informed: make(map[uint64]chan *metav1.WatchEvent),
	}
}

func (i *Informer) Handler(t *testing.T, wrapped http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.Query().Has("watch") || r.URL.Query().Get("watch") != "true" {
			wrapped.ServeHTTP(w, r)
			return
		}

		gvk, ok := parseGVK(t, r.URL.Path)
		if !ok {
			return
		}
		gvkKey := gvk.String()

		w.Header().Add("Transfer-Encoding", "chunked")
		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// Build the per-watcher loop. Its Handle method writes events to
		// this response; on Write failure it returns errClientGone and
		// Run unwinds, which we treat as "client disconnected, drain and
		// stop".
		h := &streamHandler{
			informer: i,
			t:        t,
			w:        w,
			flusher:  flusher,
		}
		l := streamFactory.NewLoop(h)

		// Register the loop under this GVK and atomically pick up any
		// backlog so subsequent inform() calls go through us.
		i.lock.Lock()
		backlog := i.backlog[gvkKey]
		i.backlog[gvkKey] = nil
		i.streams[gvkKey] = append(i.streams[gvkKey], l)
		i.lock.Unlock()

		// Pre-load backlog onto the loop; Run will process them before
		// blocking on new events.
		for _, b := range backlog {
			l.Enqueue(streamEvent{payload: b})
		}

		// Cancel the loop when the client disconnects, or after the
		// rotateInterval window, whichever comes first.
		//
		// The periodic close mirrors master's behaviour without the per-
		// event reconnect penalty: holding /watch open lets bursts of
		// explicit Informer().Add events stream back-to-back, while the
		// rotation forces the reflector to do a fresh LIST every few
		// hundred ms. That fresh LIST is what makes store-only mutations
		// (tests that call store.Add without a paired Informer().Add)
		// observable to consumers like the operator, and it lets typed
		// TypeMeta survive the trip through controller-runtime's cache,
		// which we have seen get stripped when the same item arrives via
		// the watch decode path instead of the list decode path.
		//
		// The disconnect goroutine reads loop internals via l.Close, so
		// we must wait for it to fully exit before recycling the loop,
		// otherwise the next NewLoop on the recycled instance races with
		// this Close.
		runCtx, cancel := context.WithCancel(r.Context())
		closerDone := make(chan struct{})
		go func() {
			defer close(closerDone)
			select {
			case <-runCtx.Done():
			case <-time.After(rotateInterval):
			}
			l.Close(streamEvent{closed: true})
		}()

		runErr := l.Run(runCtx)

		// Unregister before triggering Close so no further inform() calls
		// can enqueue onto a loop that's about to be recycled.
		i.lock.Lock()
		for j, c := range i.streams[gvkKey] {
			if c == l {
				i.streams[gvkKey] = append(i.streams[gvkKey][:j], i.streams[gvkKey][j+1:]...)
				break
			}
		}
		i.lock.Unlock()

		cancel()
		<-closerDone
		streamFactory.CacheLoop(l)
		_ = runErr
	}
}

// streamHandler is the per-watcher loop.Handler[streamEvent] that writes
// events to one HTTP response.
type streamHandler struct {
	informer *Informer
	t        *testing.T
	w        http.ResponseWriter
	flusher  http.Flusher
}

var errClientGone = errors.New("informer: client disconnected")

// Handle writes one streamEvent. Returns errClientGone if the HTTP write
// fails (the client closed its end), which exits the per-watcher loop.
func (h *streamHandler) Handle(_ context.Context, ev streamEvent) error {
	if ev.closed {
		return errClientGone
	}
	var event metav1.WatchEvent
	if err := json.Unmarshal(ev.payload, &event); err != nil {
		assert.NoError(h.t, err)
		return errClientGone
	}
	if _, err := h.w.Write(ev.payload); err != nil {
		return errClientGone
	}
	if h.flusher != nil {
		h.flusher.Flush()
	}

	// Snapshot informed subscribers under the lock, then notify outside
	// the lock so a slow subscriber cannot wedge inform() callers.
	h.informer.lock.Lock()
	subs := make([]chan *metav1.WatchEvent, 0, len(h.informer.informed))
	for _, c := range h.informer.informed {
		subs = append(subs, c)
	}
	h.informer.lock.Unlock()

	for _, c := range subs {
		select {
		case c <- &event:
		case <-time.After(informedSendTimeout):
			h.t.Errorf("failed to send informed event to subscriber")
		}
	}
	return nil
}

func (i *Informer) Add(t *testing.T, obj runtime.Object) {
	t.Helper()
	i.inform(t, obj, string(watch.Added))
}

func (i *Informer) Modify(t *testing.T, obj runtime.Object) {
	t.Helper()
	i.inform(t, obj, string(watch.Modified))
}

func (i *Informer) Delete(t *testing.T, obj runtime.Object) {
	t.Helper()
	i.inform(t, obj, string(watch.Deleted))
}

func (i *Informer) DeleteWait(t *testing.T, ctx context.Context, obj runtime.Object) {
	t.Helper()

	i.lock.Lock()
	//nolint:gosec
	ui := rand.Uint64()
	ch := make(chan *metav1.WatchEvent)
	i.informed[ui] = ch
	i.lock.Unlock()

	defer func() {
		i.lock.Lock()
		close(ch)
		delete(i.informed, ui)
		i.lock.Unlock()
	}()

	i.Delete(t, obj)

	exp, err := json.Marshal(obj)
	require.NoError(t, err)

	for {
		select {
		case <-ctx.Done():
			assert.Fail(t, "failed to wait for delete event to occur")
			return
		case e := <-ch:
			if e.Type != string(watch.Deleted) {
				continue
			}

			if !bytes.Equal(exp, e.Object.Raw) {
				continue
			}

			return
		}
	}
}

func (i *Informer) inform(t *testing.T, obj runtime.Object, event string) {
	t.Helper()
	gvk := i.objToGVK(t, obj)

	watchObjB, err := json.Marshal(obj)
	require.NoError(t, err)

	watchEvent, err := json.Marshal(&metav1.WatchEvent{
		Type:   event,
		Object: runtime.RawExtension{Raw: watchObjB, Object: obj},
	})
	require.NoError(t, err)

	i.lock.Lock()
	defer i.lock.Unlock()

	streams := i.streams[gvk.String()]
	if len(streams) == 0 {
		// No connected watcher: stash until one connects.
		i.backlog[gvk.String()] = append(i.backlog[gvk.String()], watchEvent)
		return
	}
	for _, l := range streams {
		// Enqueue is non-blocking and the loop's queue is unbounded.
		l.Enqueue(streamEvent{payload: watchEvent})
	}
}

// parseGVK pulls the GroupVersionKind out of a Kubernetes watch URL of the
// form /apis/<group>/<version>/[namespaces/<ns>/]<kind> or
// /api/v1/[namespaces/<ns>/]<kind>.
func parseGVK(t *testing.T, urlPath string) (schema.GroupVersionKind, bool) {
	t.Helper()
	path := strings.TrimPrefix(strings.TrimPrefix(urlPath, "/apis/"), "/api/")
	split := strings.Split(path, "/")
	if !assert.GreaterOrEqual(t, len(split), 2, "invalid path: %s", path) {
		return schema.GroupVersionKind{}, false
	}

	var gvk schema.GroupVersionKind
	if split[0] == "v1" {
		gvk = schema.GroupVersionKind{Group: "", Version: "v1"}
		split = split[1:]
	} else {
		gvk = schema.GroupVersionKind{Group: split[0], Version: split[1]}
		split = split[2:]
	}
	if !assert.NotEmpty(t, split, "invalid path: %s", path) {
		return schema.GroupVersionKind{}, false
	}
	if split[0] == "namespaces" {
		switch {
		case len(split) == 1:
			gvk.Kind = "namespaces"
		case len(split) >= 3:
			gvk.Kind = split[2]
		default:
			assert.Fail(t, "invalid path: missing kind", path)
			return schema.GroupVersionKind{}, false
		}
	} else {
		gvk.Kind = split[0]
	}
	return gvk, true
}

func (i *Informer) objToGVK(t *testing.T, obj runtime.Object) schema.GroupVersionKind {
	t.Helper()

	switch obj.(type) {
	case *appsv1.Deployment:
		return schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "deployments"}
	case *compapi.Component:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "components"}
	case *configapi.Configuration:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "configurations"}
	case *httpendapi.HTTPEndpoint:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "httpendpoints"}
	case *mcpserverapi.MCPServer:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "mcpservers"}
	case *resiliencyapi.Resiliency:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "resiliencies"}
	case *subapi.Subscription:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v2alpha1", Kind: "subscriptions"}
	case *wfaclapi.WorkflowAccessPolicy:
		return schema.GroupVersionKind{Group: "dapr.io", Version: "v1alpha1", Kind: "workflowaccesspolicies"}
	case *corev1.Pod:
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "pods"}
	case *corev1.Service:
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "services"}
	case *corev1.Secret:
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "secrets"}
	case *corev1.ConfigMap:
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "configmaps"}
	case *corev1.Namespace:
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: "namespaces"}
	default:
		require.Failf(t, "unknown type", "%T", obj)
		return schema.GroupVersionKind{}
	}
}
