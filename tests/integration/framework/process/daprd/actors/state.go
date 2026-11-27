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

package actors

import (
	"context"
	"io"
	nethttp "net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/client"
)

// ActorHTTP sends a request to the actor HTTP API of daprd, at
// /v1.0/actors/<actorType>/<actorID><path>, and returns the status code and
// body of the response. t may be an assert.CollectT.
func (a *Actors) ActorHTTP(t require.TestingT, ctx context.Context, method, actorType, actorID, path, body string) (int, string) {
	url := "http://" + a.daprd.HTTPAddress() + "/v1.0/actors/" + actorType + "/" + actorID + path
	req, err := nethttp.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := a.httpClient().Do(req)
	require.NoError(t, err)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp.StatusCode, string(b)
}

// InvokeActor activates the actor by calling its method foo over HTTP and
// over gRPC.
func (a *Actors) InvokeActor(t require.TestingT, ctx context.Context, actorType, actorID string) {
	code, body := a.ActorHTTP(t, ctx, nethttp.MethodPost, actorType, actorID, "/method/foo", "")
	require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)
	_, err := a.stateClient(t).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: actorType, ActorId: actorID, Method: "foo",
	})
	require.NoError(t, err)
}

// SaveActorStateHTTP upserts key with value through the HTTP actor state
// API, and returns the status code and body of the response.
func (a *Actors) SaveActorStateHTTP(t require.TestingT, ctx context.Context, actorType, actorID, key, value string) (int, string) {
	return a.ActorHTTP(t, ctx, nethttp.MethodPost, actorType, actorID, "/state",
		`[{"operation":"upsert","request":{"key":"`+key+`","value":"`+value+`"}}]`)
}

// SaveActorStateGRPC upserts key with value through the gRPC actor state
// API.
func (a *Actors) SaveActorStateGRPC(t require.TestingT, ctx context.Context, actorType, actorID, key, value string) error {
	_, err := a.stateClient(t).ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: actorType,
		ActorId:   actorID,
		Operations: []*rtv1.TransactionalActorStateOperation{{
			OperationType: "upsert",
			Key:           key,
			Value:         &anypb.Any{Value: []byte(value)},
		}},
	})
	return err
}

// GetActorStateGRPC reads key through the gRPC actor state API.
func (a *Actors) GetActorStateGRPC(t require.TestingT, ctx context.Context, actorType, actorID, key string) (string, error) {
	res, err := a.stateClient(t).GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: actorType, ActorId: actorID, Key: key,
	})
	return string(res.GetData()), err
}

// SaveAndReadActorStateHTTP activates the actor, then writes and reads back
// key k1 through the HTTP API.
func (a *Actors) SaveAndReadActorStateHTTP(t *testing.T, ctx context.Context, actorType, actorID string) {
	t.Helper()

	code, body := a.ActorHTTP(t, ctx, nethttp.MethodPost, actorType, actorID, "/method/foo", "")
	require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)

	code, body = a.SaveActorStateHTTP(t, ctx, actorType, actorID, "k1", "v1")
	require.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)

	code, body = a.ActorHTTP(t, ctx, nethttp.MethodGet, actorType, actorID, "/state/k1", "")
	require.Equalf(t, nethttp.StatusOK, code, "body: %s", body)
	assert.JSONEq(t, `"v1"`, body)
}

// SaveAndReadActorStateGRPC activates the actor, then writes and reads back
// key k1 through the gRPC API.
func (a *Actors) SaveAndReadActorStateGRPC(t *testing.T, ctx context.Context, actorType, actorID string) {
	t.Helper()

	_, err := a.stateClient(t).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: actorType, ActorId: actorID, Method: "foo",
	})
	require.NoError(t, err)

	require.NoError(t, a.SaveActorStateGRPC(t, ctx, actorType, actorID, "k1", "v1"))

	got, err := a.GetActorStateGRPC(t, ctx, actorType, actorID, "k1")
	require.NoError(t, err)
	assert.Equal(t, "v1", got)
}

// stateClient returns a gRPC client to daprd which is shared by every helper
// call and closed on Cleanup, so helpers retried in a loop do not each dial
// a connection.
func (a *Actors) stateClient(t require.TestingT) rtv1.DaprClient {
	a.clientOnce.Do(func() {
		a.conn, a.connErr = grpc.NewClient(a.daprd.GRPCAddress(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	})
	require.NoError(t, a.connErr)
	return rtv1.NewDaprClient(a.conn)
}

func (a *Actors) httpClient() *nethttp.Client {
	a.httpOnce.Do(func() {
		// Cleanup closes its idle connections.
		a.http = client.HTTPWithTimeout(nil, 30*time.Second)
	})
	return a.http
}
