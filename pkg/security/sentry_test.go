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
	"net"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

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
