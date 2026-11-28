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

package security

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opencensus.io/stats/view"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/diagnostics"
	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/kit/crypto/spiffe"
)

type fetchJWTServer struct {
	sentryv1pb.UnimplementedCAServer
	gotAudiences []string
}

func (f *fetchJWTServer) FetchJWTSVID(_ context.Context, req *sentryv1pb.FetchJWTSVIDRequest) (*sentryv1pb.FetchJWTSVIDResponse, error) {
	f.gotAudiences = req.GetAudiences()
	return &sentryv1pb.FetchJWTSVIDResponse{Jwt: "my-jwt"}, nil
}

func Test_newJWTRequestFn(t *testing.T) {
	serve := func(t *testing.T, srv sentryv1pb.CAServer) string {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		gs := grpc.NewServer()
		sentryv1pb.RegisterCAServer(gs, srv)
		go gs.Serve(lis)
		t.Cleanup(gs.Stop)
		return lis.Addr().String()
	}

	newFn := func(t *testing.T, addr string) spiffe.RequestJWTSVIDFn {
		// A security without a SPIFFE identity dials insecurely.
		fn, err := newJWTRequestFn(Options{
			SentryAddress:         addr,
			ControlPlaneNamespace: "dapr-system",
		}, new(security), spiffeid.RequireTrustDomainFromString("cluster.local"))
		require.NoError(t, err)
		return fn
	}

	t.Run("returns the minted JWT", func(t *testing.T) {
		srv := new(fetchJWTServer)
		jwt, err := newFn(t, serve(t, srv))(t.Context(), []string{"a", "b"})
		require.NoError(t, err)
		assert.Equal(t, "my-jwt", jwt)
		assert.Equal(t, []string{"a", "b"}, srv.gotAudiences)
	})

	t.Run("older sentry asks for a control plane upgrade", func(t *testing.T) {
		_, err := newFn(t, serve(t, new(sentryv1pb.UnimplementedCAServer)))(t.Context(), []string{"a"})
		require.ErrorContains(t, err, "upgrade the Dapr control plane")
	})
}

func Test_withRotationMetrics(t *testing.T) {
	meter := view.NewMeter()
	meter.Start()
	t.Cleanup(meter.Stop)
	require.NoError(t, diagnostics.DefaultMonitoring.Init(meter, "test", view.Distribution(1)))

	rotated := func(t *testing.T) int64 {
		t.Helper()
		rows, err := meter.RetrieveData("runtime/mtls/workload_cert_rotated_total")
		require.NoError(t, err)
		if len(rows) == 0 {
			return 0
		}
		require.Len(t, rows, 1)
		return rows[0].Data.(*view.CountData).Value
	}

	var reqErr error
	fn := withRotationMetrics(func(context.Context, []byte) (*spiffe.SVIDResponse, error) {
		if reqErr != nil {
			return nil, reqErr
		}
		return new(spiffe.SVIDResponse), nil
	})

	_, err := fn(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), rotated(t), "initial identity is not a rotation")

	reqErr = errors.New("sentry unavailable")
	_, err = fn(t.Context(), nil)
	require.Error(t, err)
	assert.Equal(t, int64(0), rotated(t), "failed request is not a rotation")

	reqErr = nil
	_, err = fn(t.Context(), nil)
	require.NoError(t, err)
	_, err = fn(t.Context(), nil)
	require.NoError(t, err)
	assert.Equal(t, int64(2), rotated(t))
}
