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

package grpc

import (
	"context"
	"io"
	nethttp "net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/actors/etag"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(stalecache))
}

// stalecache mimics an SDK that caches etags per activation and never re-reads.
type stalecache struct {
	app    *actors.Actors
	grpcCl rtv1.DaprClient

	lock  sync.Mutex
	cache map[string]cached
	reads atomic.Int64

	read    map[string]chan struct{}
	release chan struct{}
	saved   map[string]chan string
}

type cached struct {
	activationID string
	etag         string
}

func (s *stalecache) Setup(t *testing.T) []framework.Option {
	s.cache = make(map[string]cached)
	s.release = make(chan struct{})
	s.read = map[string]chan struct{}{"1": make(chan struct{}), "2": make(chan struct{})}
	s.saved = map[string]chan string{"1": make(chan string, 1), "2": make(chan string, 1)}

	s.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}/method/write", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			value, _ := io.ReadAll(r.Body)
			w.Write([]byte(s.write(r.Context(), r.PathValue("id"), activation.FromHeader(r), string(value))))
		}),
		actors.WithHandler("/actors/abc/{id}/method/slow", func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			id, act := r.PathValue("id"), activation.FromHeader(r)
			tag, err := s.cachedETag(r.Context(), id, act)
			if err != nil {
				s.saved[id] <- err.Error()
				return
			}
			close(s.read[id])
			<-s.release
			s.saved[id] <- s.save(context.Background(), id, act, "stale", tag)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(s.app),
	}
}

func (s *stalecache) Run(t *testing.T, ctx context.Context) {
	s.app.WaitUntilRunning(t, ctx)
	s.grpcCl = s.app.GRPCClient(t, ctx)

	write := func(id, value string) string {
		return string(activation.Invoke(t, ctx, s.app.GRPCClient(t, ctx), &rtv1.InvokeActorRequest{
			ActorType: "abc", ActorId: id, Method: "write", Data: []byte(value),
		}))
	}

	assert.Equal(t, "ok", write("1", "v1"))
	assert.Equal(t, "ok", write("1", "v1b"))
	assert.Equal(t, "ok", write("1", "v1c"))
	assert.Equal(t, int64(1), s.reads.Load())
	assert.Equal(t, "v1c", s.value(t, ctx, "1"))

	slowCl := s.app.GRPCClient(t, ctx)
	for _, id := range []string{"1", "2"} {
		go func() {
			_, _ = slowCl.InvokeActor(ctx, &rtv1.InvokeActorRequest{ActorType: "abc", ActorId: id, Method: "slow"})
		}()
		select {
		case <-s.read[id]:
		case <-time.After(time.Second * 10):
			require.Fail(t, "slow callback never got its cached etag", id)
		}
	}
	assert.Equal(t, int64(2), s.reads.Load())

	s.app.Daprd().Restart(t, ctx)
	s.app.Daprd().WaitUntilRunning(t, ctx)
	s.app.Daprd().WaitUntilActorTypeHosted(t, ctx, "abc")

	assert.Equal(t, "ok", write("1", "v2"))
	assert.Equal(t, "ok", write("2", "v2"))
	assert.Equal(t, int64(4), s.reads.Load())

	want := "Aborted"
	close(s.release)
	for _, id := range []string{"1", "2"} {
		select {
		case got := <-s.saved[id]:
			assert.Equal(t, want, got, id)
		case <-time.After(time.Second * 10):
			require.Fail(t, "slow callback never saved", id)
		}
		assert.Equal(t, "v2", s.value(t, ctx, id))
	}
}

func (s *stalecache) write(ctx context.Context, id, act, value string) string {
	tag, err := s.cachedETag(ctx, id, act)
	if err != nil {
		return err.Error()
	}
	return s.save(ctx, id, act, value, tag)
}

// An empty etag means the key was absent.
func (s *stalecache) cachedETag(ctx context.Context, id, act string) (string, error) {
	s.lock.Lock()
	c, ok := s.cache[id]
	s.lock.Unlock()
	if ok && c.activationID == act {
		return c.etag, nil
	}

	s.reads.Add(1)
	_, tag, err := etag.GetGRPC(ctx, s.grpcCl, "abc", id, "k")
	if err != nil {
		return "", err
	}
	s.setCache(id, act, tag)
	return tag, nil
}

func (s *stalecache) setCache(id, act, tag string) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.cache[id] = cached{activationID: act, etag: tag}
}

func (s *stalecache) value(t *testing.T, ctx context.Context, id string) string {
	t.Helper()
	v, _, err := etag.GetGRPC(ctx, s.app.GRPCClient(t, ctx), "abc", id, "k")
	require.NoError(t, err)
	return v
}

func (s *stalecache) save(ctx context.Context, id, act, value, tag string) string {
	etags, code, err := etag.SaveGRPC(ctx, s.grpcCl, "abc", id, true, etag.Op{Key: "k", Value: value, ETag: tag, CreateOnly: tag == ""})
	if err != nil {
		return err.Error()
	}
	if code != "ok" {
		return code
	}
	if etags["k"] == "" {
		return "no etag returned"
	}
	s.setCache(id, act, etags["k"])
	return "ok"
}
