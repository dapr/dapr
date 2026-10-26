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
	suite.Register(new(staleetag))
}

// staleetag kills daprd between an old activation's read and save.
type staleetag struct {
	app     *actors.Actors
	grpcCl  rtv1.DaprClient
	read    chan struct{}
	release chan struct{}
	saved   chan string
}

func (s *staleetag) Setup(t *testing.T) []framework.Option {
	s.read = make(chan struct{})
	s.release = make(chan struct{})
	s.saved = make(chan string, 1)

	s.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}/method/write", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			value, _ := io.ReadAll(r.Body)
			_, tag, err := etag.GetGRPC(r.Context(), s.grpcCl, "abc", r.PathValue("id"), "k")
			if err != nil {
				nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
				return
			}
			w.Write([]byte(s.save(r.Context(), r.PathValue("id"), string(value), tag)))
		}),
		actors.WithHandler("/actors/abc/{id}/method/slow", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			_, tag, err := etag.GetGRPC(r.Context(), s.grpcCl, "abc", r.PathValue("id"), "k")
			if err != nil {
				s.saved <- err.Error()
				return
			}
			close(s.read)
			<-s.release
			s.saved <- s.save(context.Background(), r.PathValue("id"), "stale", tag)
		}),
	)

	return []framework.Option{
		framework.WithProcesses(s.app),
	}
}

func (s *staleetag) Run(t *testing.T, ctx context.Context) {
	s.app.WaitUntilRunning(t, ctx)
	s.grpcCl = s.app.GRPCClient(t, ctx)

	write := func(value string) string {
		return string(activation.Invoke(t, ctx, s.app.GRPCClient(t, ctx), &rtv1.InvokeActorRequest{
			ActorType: "abc", ActorId: "1", Method: "write", Data: []byte(value),
		}))
	}

	assert.Equal(t, "ok", write("v1"))
	assert.Equal(t, "v1", s.value(t, ctx))

	slowCl := s.app.GRPCClient(t, ctx)
	go func() {
		_, _ = slowCl.InvokeActor(ctx, &rtv1.InvokeActorRequest{ActorType: "abc", ActorId: "1", Method: "slow"})
	}()
	select {
	case <-s.read:
	case <-time.After(time.Second * 10):
		require.Fail(t, "slow callback never read state")
	}

	s.app.Daprd().Restart(t, ctx)
	s.app.Daprd().WaitUntilRunning(t, ctx)
	s.app.Daprd().WaitUntilActorTypeHosted(t, ctx, "abc")

	assert.Equal(t, "ok", write("v2"))

	close(s.release)
	select {
	case got := <-s.saved:
		assert.Equal(t, "Aborted", got)
	case <-time.After(time.Second * 10):
		require.Fail(t, "slow callback never saved")
	}

	assert.Equal(t, "v2", s.value(t, ctx))
}

func (s *staleetag) value(t *testing.T, ctx context.Context) string {
	t.Helper()
	v, _, err := etag.GetGRPC(ctx, s.app.GRPCClient(t, ctx), "abc", "1", "k")
	require.NoError(t, err)
	return v
}

func (s *staleetag) save(ctx context.Context, id, value, tag string) string {
	_, code, err := etag.SaveGRPC(ctx, s.grpcCl, "abc", id, false, etag.Op{Key: "k", Value: value, ETag: tag})
	if err != nil {
		return err.Error()
	}
	return code
}
