/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd1 *procdaprd.Daprd
	daprd2 *procdaprd.Daprd
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		switch in.GetMethod() {
		case "foo":
			var verb int
			var data []byte
			switch in.GetHttpExtension().GetVerb() {
			case commonv1.HTTPExtension_PATCH:
				data = []byte("PATCH")
				verb = http.StatusNoContent
			case commonv1.HTTPExtension_POST:
				data = []byte("POST")
				verb = http.StatusCreated
			case commonv1.HTTPExtension_GET:
				data = []byte("GET")
				verb = http.StatusOK
			case commonv1.HTTPExtension_PUT:
				data = []byte("PUT")
				verb = http.StatusAccepted
			case commonv1.HTTPExtension_DELETE:
				data = []byte("DELETE")
				verb = http.StatusConflict
			}
			return &commonv1.InvokeResponse{
				Data:        &anypb.Any{Value: data},
				ContentType: strconv.Itoa(verb),
			}, nil

		case "typed":
			return &commonv1.InvokeResponse{
				Data: &anypb.Any{
					Value:   []byte(fmt.Sprintf("%s/%s", string(in.GetData().GetValue()), in.GetData().GetTypeUrl())),
					TypeUrl: "mytype",
				},
				ContentType: "application/json",
			}, nil

		case "multiple/segments":
			return &commonv1.InvokeResponse{
				Data:        &anypb.Any{Value: []byte("multiple/segments")},
				ContentType: "application/json",
			}, nil

		default:
			return nil, errors.New("invalid method")
		}
	}

	srv1 := newGRPCServer(t, onInvoke)
	srv2 := newGRPCServer(t, onInvoke)
	b.daprd1 = procdaprd.New(t,
		procdaprd.WithAppProtocol("grpc"),
		procdaprd.WithAppPort(srv1.Port(t)),
	)
	b.daprd2 = procdaprd.New(t,
		procdaprd.WithAppProtocol("grpc"),
		procdaprd.WithAppPort(srv2.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, b.daprd1, b.daprd2),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd1.WaitUntilRunning(t, ctx)
	b.daprd2.WaitUntilRunning(t, ctx)

	t.Run("invoke host", func(t *testing.T) {
		doReq := func(host, hostID string, verb commonv1.HTTPExtension_Verb) ([]byte, string) {
			conn, err := grpc.DialContext(ctx, host,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock(),
			)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })

			resp, err := rtv1.NewDaprClient(conn).InvokeService(ctx, &rtv1.InvokeServiceRequest{
				Id: hostID,
				Message: &commonv1.InvokeRequest{
					Method:        "foo",
					HttpExtension: &commonv1.HTTPExtension{Verb: verb},
				},
			})
			require.NoError(t, err)

			return resp.GetData().GetValue(), resp.GetContentType()
		}

		for _, ts := range []struct {
			host   string
			hostID string
		}{
			{host: b.daprd1.GRPCAddress(), hostID: b.daprd2.AppID()},
			{host: b.daprd2.GRPCAddress(), hostID: b.daprd1.AppID()},
		} {
			t.Run(ts.host, func(t *testing.T) {
				body, contentType := doReq(ts.host, ts.hostID, commonv1.HTTPExtension_GET)
				assert.Equal(t, "GET", string(body))
				assert.Equal(t, "200", contentType)

				body, contentType = doReq(ts.host, ts.hostID, commonv1.HTTPExtension_POST)
				assert.Equal(t, "POST", string(body))
				assert.Equal(t, "201", contentType)

				body, contentType = doReq(ts.host, ts.hostID, commonv1.HTTPExtension_PUT)
				assert.Equal(t, "PUT", string(body))
				assert.Equal(t, "202", contentType)

				body, contentType = doReq(ts.host, ts.hostID, commonv1.HTTPExtension_DELETE)
				assert.Equal(t, "DELETE", string(body))
				assert.Equal(t, "409", contentType)

				body, contentType = doReq(ts.host, ts.hostID, commonv1.HTTPExtension_PATCH)
				assert.Equal(t, "PATCH", string(body))
				assert.Equal(t, "204", contentType)
			})
		}
	})

	t.Run("method doesn't exist", func(t *testing.T) {
		host := b.daprd1.GRPCAddress()
		conn, err := grpc.DialContext(ctx, host,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		resp, err := rtv1.NewDaprClient(conn).InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: b.daprd2.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        "doesnotexist",
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
			},
		})
		require.Error(t, err)
		// TODO: this should be codes.NotFound.
		assert.Equal(t, codes.Unknown, status.Convert(err).Code())
		assert.Equal(t, "invalid method", status.Convert(err).Message())
		assert.Nil(t, resp)
	})

	t.Run("no method", func(t *testing.T) {
		host := b.daprd1.GRPCAddress()
		conn, err := grpc.DialContext(ctx, host,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		resp, err := rtv1.NewDaprClient(conn).InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: b.daprd2.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        "",
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
			},
		})
		require.Error(t, err)
		// TODO: this should be codes.InvalidArgument.
		assert.Equal(t, codes.Unknown, status.Convert(err).Code())
		assert.Equal(t, "invalid method", status.Convert(err).Message())
		assert.Nil(t, resp)
	})

	t.Run("multiple segments", func(t *testing.T) {
		host := b.daprd1.GRPCAddress()
		conn, err := grpc.DialContext(ctx, host,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		resp, err := rtv1.NewDaprClient(conn).InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: b.daprd2.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        "multiple/segments",
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "multiple/segments", string(resp.GetData().GetValue()))
		assert.Equal(t, "application/json", resp.GetContentType())
	})

	pt := util.NewParallel(t)
	for i := 0; i < 100; i++ {
		pt.Add(func(c *assert.CollectT) {
			host := b.daprd1.GRPCAddress()
			conn, err := grpc.DialContext(ctx, host, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
			require.NoError(c, err)
			t.Cleanup(func() { require.NoError(c, conn.Close()) })

			resp, err := rtv1.NewDaprClient(conn).InvokeService(ctx, &rtv1.InvokeServiceRequest{
				Id: b.daprd2.AppID(),
				Message: &commonv1.InvokeRequest{
					Method:        "foo",
					HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_POST},
				},
			})
			require.NoError(c, err)

			assert.Equal(c, "POST", string(resp.GetData().GetValue()))
			assert.Equal(c, "201", resp.GetContentType())
		})
	}

	t.Run("type URL", func(t *testing.T) {
		host := b.daprd1.GRPCAddress()
		conn, err := grpc.DialContext(ctx, host,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })

		resp, err := rtv1.NewDaprClient(conn).InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: b.daprd2.AppID(),
			Message: &commonv1.InvokeRequest{
				Method: "typed",
				Data: &anypb.Any{
					Value:   []byte("emozioni di settembre"),
					TypeUrl: "pfm",
				},
			},
		})
		require.NoError(t, err)
		require.NotNil(t, resp.GetData())
		assert.Equal(t, "emozioni di settembre/pfm", string(resp.GetData().GetValue()))
		assert.Equal(t, "mytype", resp.GetData().GetTypeUrl())
	})
}
