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

package metadata

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sentrypbv1 "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(insecureValidator))
}

// insecureValidator tests Sentry with the insecure validator.
type insecureValidator struct {
	// Instance of Sentry that is configured with the insecure validator
	sentryWithInsecure *procsentry.Sentry
}

func (m *insecureValidator) Setup(t *testing.T) []framework.Option {
	m.sentryWithInsecure = procsentry.New(t)
	return []framework.Option{
		framework.WithProcesses(m.sentryWithInsecure),
	}
}

func (m *insecureValidator) Run(t *testing.T, parentCtx context.Context) {
	t.Run("insecure validator", func(t *testing.T) {
		var client sentrypbv1.CAClient

		t.Run("connect to Sentry", func(t *testing.T) {
			// We need to set up the TLS configuration to validate the TLS certificate provided by Sentry
			bundle := m.sentryWithInsecure.CABundle()
			sentrySpiffeID, err := spiffeid.FromString("spiffe://localhost/ns/default/dapr-sentry")
			require.NoError(t, err, "failed to create Sentry SPIFFE ID")
			x509bundle, err := x509bundle.Parse(sentrySpiffeID.TrustDomain(), bundle.TrustAnchors)
			require.NoError(t, err, "failed to create x509 bundle")
			transportCredentials := grpccredentials.TLSClientCredentials(x509bundle, tlsconfig.AuthorizeID(sentrySpiffeID))

			// Actually establish the connection using gRPC
			t.Logf("Connecting to Sentry on 127.0.0.1:%d", m.sentryWithInsecure.Port())
			ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
			defer cancel()
			conn, err := grpc.DialContext(
				ctx,
				fmt.Sprintf("127.0.0.1:%d", m.sentryWithInsecure.Port()),
				grpc.WithTransportCredentials(transportCredentials),
				grpc.WithReturnConnectionError(),
				grpc.WithBlock(),
			)
			require.NoError(t, err)

			client = sentrypbv1.NewCAClient(conn)
		})

		t.Run("fails when not passing an invalid validator", func(t *testing.T) {
			ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
			defer cancel()
			_, err := client.SignCertificate(ctx, &sentrypbv1.SignCertificateRequest{
				Id:             "myapp",
				Namespace:      "default",
				TokenValidator: sentrypbv1.SignCertificateRequest_TokenValidator(-1), // -1 is an invalid enum value
			})
			require.Error(t, err)

			grpcStatus, ok := status.FromError(err)
			require.True(t, ok)
			require.Equal(t, codes.InvalidArgument, grpcStatus.Code())
			require.Contains(t, grpcStatus.Message(), "not enabled")
		})
	})
}
