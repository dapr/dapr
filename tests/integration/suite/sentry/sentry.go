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

	"github.com/spiffe/go-spiffe/v2/bundle/x509bundle"
	"github.com/spiffe/go-spiffe/v2/spiffegrpc/grpccredentials"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/spiffe/go-spiffe/v2/spiffetls/tlsconfig"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

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
	proc *procsentry.Sentry
}

func (m *insecureValidator) Setup(t *testing.T) []framework.Option {
	m.proc = procsentry.New(t)
	return []framework.Option{
		framework.WithProcesses(m.proc),
	}
}

func (m *insecureValidator) Run(t *testing.T, ctx context.Context) {
	bundle := m.proc.CABundle()

	sentrySpiffeID, err := spiffeid.FromString("spiffe://localhost/ns/default/dapr-sentry")
	require.NoError(t, err, "failed to create Sentry SPIFFE ID")
	x509bundle, err := x509bundle.Parse(sentrySpiffeID.TrustDomain(), bundle.TrustAnchors)
	require.NoError(t, err, "failed to create x509 bundle")
	transportCredentials := grpccredentials.TLSClientCredentials(x509bundle, tlsconfig.AuthorizeID(sentrySpiffeID))

	t.Logf("Connecting to Sentry on 127.0.0.1:%d", m.proc.Port())
	conn, err := grpc.DialContext(
		ctx,
		fmt.Sprintf("127.0.0.1:%d", m.proc.Port()),
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithReturnConnectionError(),
		grpc.WithBlock(),
	)
	require.NoError(t, err)

	client := sentrypbv1.NewCAClient(conn)

	_ = client
}
