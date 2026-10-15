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

package restart

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/actors/activation"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(&staleetagHTTP{staleetag{viaGRPC: false}})
	suite.Register(&staleetagGRPC{staleetag{viaGRPC: true}})
}

type (
	staleetagHTTP struct{ staleetag }
	staleetagGRPC struct{ staleetag }
)

// staleetag kills daprd between an old activation's read and save.
type staleetag struct {
	viaGRPC bool

	app     *actors.Actors
	httpCl  *http.Client
	grpcCl  rtv1.DaprClient
	read    chan struct{}
	release chan struct{}
	saved   chan string
}

func (s *staleetag) Setup(t *testing.T) []framework.Option {
	s.httpCl = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	s.read = make(chan struct{})
	s.release = make(chan struct{})
	s.saved = make(chan string, 1)

	s.app = actors.New(t,
		actors.WithActorTypes("abc"),
		actors.WithHandler("/actors/abc/{id}/method/write", func(w http.ResponseWriter, r *http.Request) {
			value, _ := io.ReadAll(r.Body)
			etag, err := s.get(r.Context(), r.PathValue("id"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Write([]byte(s.save(r.Context(), r.PathValue("id"), string(value), etag)))
		}),
		actors.WithHandler("/actors/abc/{id}/method/slow", func(w http.ResponseWriter, r *http.Request) {
			etag, err := s.get(r.Context(), r.PathValue("id"))
			if err != nil {
				s.saved <- err.Error()
				return
			}
			close(s.read)
			<-s.release
			s.saved <- s.save(context.Background(), r.PathValue("id"), "stale", etag)
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
		if s.viaGRPC {
			assert.Equal(t, "Aborted", got)
		} else {
			assert.Equal(t, "409 ERR_ACTOR_STATE_ETAG_MISMATCH", got)
		}
	case <-time.After(time.Second * 10):
		require.Fail(t, "slow callback never saved")
	}

	assert.Equal(t, "v2", s.value(t, ctx))
}

func (s *staleetag) value(t *testing.T, ctx context.Context) string {
	t.Helper()
	resp, err := s.app.GRPCClient(t, ctx).GetActorState(ctx, &rtv1.GetActorStateRequest{ActorType: "abc", ActorId: "1", Key: "k"})
	require.NoError(t, err)
	return strings.Trim(string(resp.GetData()), `"`)
}

func (s *staleetag) get(ctx context.Context, id string) (string, error) {
	if s.viaGRPC {
		resp, err := s.grpcCl.GetActorState(ctx, &rtv1.GetActorStateRequest{ActorType: "abc", ActorId: id, Key: "k"}, grpc.WaitForReady(true))
		if err != nil {
			return "", err
		}
		return resp.GetEtag(), nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+s.app.Daprd().HTTPAddress()+"/v1.0/actors/abc/"+id+"/state/k", nil)
	if err != nil {
		return "", err
	}
	resp, err := s.httpCl.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("get state: %d", resp.StatusCode)
	}
	return resp.Header.Get("ETag"), nil
}

// save returns "ok" or the gRPC code or the HTTP status and error code.
func (s *staleetag) save(ctx context.Context, id, value, etag string) string {
	if s.viaGRPC {
		_, err := s.grpcCl.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
			ActorType: "abc",
			ActorId:   id,
			Operations: []*rtv1.TransactionalActorStateOperation{{
				OperationType: "upsert", Key: "k", Value: &anypb.Any{Value: []byte(value)}, Etag: etag,
			}},
		}, grpc.WaitForReady(true))
		if err != nil {
			return status.Code(err).String()
		}
		return "ok"
	}

	op := map[string]any{"key": "k", "value": value}
	if etag != "" {
		op["etag"] = etag
	}
	body, err := json.Marshal([]map[string]any{{"operation": "upsert", "request": op}})
	if err != nil {
		return err.Error()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+s.app.Daprd().HTTPAddress()+"/v1.0/actors/abc/"+id+"/state", strings.NewReader(string(body)))
	if err != nil {
		return err.Error()
	}
	resp, err := s.httpCl.Do(req)
	if err != nil {
		return err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return "ok"
	}
	var apiErr struct {
		ErrorCode string `json:"errorCode"`
	}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &apiErr)
	return fmt.Sprintf("%d %s", resp.StatusCode, apiErr.ErrorCode)
}
