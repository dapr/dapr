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

package quorum

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/healthz"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(jwks))
}

// jwks tests placement can find quorum with tls (jwks) enabled.
type jwks struct {
	places       []*placement.Placement
	sentry       *sentry.Sentry
	appTokenFile string
}

func (j *jwks) Setup(t *testing.T) []framework.Option {
	jwtPriv, jwtPub := j.genPrivateJWK(t)

	tokenFiles := make([]string, 3)
	for i := range tokenFiles {
		token := j.signJWT(t, jwtPriv, "spiffe://localhost/ns/default/dapr-placement")
		tokenFiles[i] = filepath.Join(t.TempDir(), "token-"+strconv.Itoa(i))
		require.NoError(t, os.WriteFile(tokenFiles[i], token, 0o600))
	}

	j.appTokenFile = filepath.Join(t.TempDir(), "app-token")
	require.NoError(t, os.WriteFile(j.appTokenFile,
		j.signJWT(t, jwtPriv, "spiffe://public/ns/default/app-1"),
		0o600),
	)

	jwksConfig := `
kind: Configuration
apiVersion: dapr.io/v1alpha1
metadata:
  name: sentryconfig
spec:
  mtls:
    enabled: true
    tokenValidators:
      - name: jwks
        options:
          minRefreshInterval: 2m
          requestTimeout: 1m
          source: |
            {"keys":[` + string(jwtPub) + `]}
`
	j.sentry = sentry.New(t, sentry.WithConfiguration(jwksConfig))
	bundle := j.sentry.CABundle()

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, bundle.X509.TrustAnchors, 0o600))

	fp := ports.Reserve(t, 3)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)
	opts := []placement.Option{
		placement.WithInitialCluster(fmt.Sprintf("p1=localhost:%d,p2=localhost:%d,p3=localhost:%d", port1, port2, port3)),
		placement.WithInitialClusterPorts(port1, port2, port3),
		placement.WithEnableTLS(true),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithSentryAddress(j.sentry.Address()),
	}
	j.places = []*placement.Placement{
		placement.New(t, append(opts, placement.WithID("p1"),
			placement.WithExecOptions(exec.WithEnvVars(t, "DAPR_SENTRY_TOKEN_FILE", tokenFiles[0])))...),
		placement.New(t, append(opts, placement.WithID("p2"),
			placement.WithExecOptions(exec.WithEnvVars(t, "DAPR_SENTRY_TOKEN_FILE", tokenFiles[1])))...),
		placement.New(t, append(opts, placement.WithID("p3"),
			placement.WithExecOptions(exec.WithEnvVars(t, "DAPR_SENTRY_TOKEN_FILE", tokenFiles[2])))...),
	}

	return []framework.Option{
		framework.WithProcesses(j.sentry, fp, j.places[0], j.places[1], j.places[2]),
	}
}

func (j *jwks) Run(t *testing.T, ctx context.Context) {
	j.sentry.WaitUntilRunning(t, ctx)
	j.places[0].WaitUntilRunning(t, ctx)
	j.places[1].WaitUntilRunning(t, ctx)
	j.places[2].WaitUntilRunning(t, ctx)

	secProv, err := security.New(ctx, security.Options{
		SentryAddress:           j.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            j.sentry.CABundle().X509.TrustAnchors,
		AppID:                   "app-1",
		MTLSEnabled:             true,
		SentryTokenFile:         ptr.Of(j.appTokenFile),
		Healthz:                 healthz.New(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- secProv.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); require.NoError(t, <-errCh) })

	sec, err := secProv.Handler(ctx)
	require.NoError(t, err)

	placeID, err := spiffeid.FromSegments(sec.ControlPlaneTrustDomain(), "ns", "default", "dapr-placement")
	require.NoError(t, err)

	var stream v1pb.Placement_ReportDaprStatusClient

	// Try connecting to each placement until one succeeds,
	// indicating that a leader has been elected
	i := -1
	require.Eventually(t, func() bool {
		i++
		if i >= 3 {
			i = 0
		}
		host := j.places[i].Address()
		conn, cerr := grpc.DialContext(ctx, host, grpc.WithBlock(), //nolint:staticcheck
			grpc.WithReturnConnectionError(), sec.GRPCDialOptionMTLS(placeID), //nolint:staticcheck
		)
		if cerr != nil {
			return false
		}
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		client := v1pb.NewPlacementClient(conn)

		stream, err = client.ReportDaprStatus(ctx)
		if err != nil {
			return false
		}
		err = stream.Send(&v1pb.Host{Id: "app-1"})
		if err != nil {
			return false
		}
		_, err = stream.Recv()
		if err != nil {
			return false
		}
		return true
	}, time.Second*10, time.Millisecond*10)

	err = stream.Send(&v1pb.Host{
		Name:      "app-1",
		Namespace: "default",
		Port:      1234,
		Load:      1,
		Entities:  []string{"entity-1", "entity-2"},
		Id:        "app-1",
		Pod:       "pod-1",
	})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		o, err := stream.Recv()
		if !assert.NoError(c, err) {
			return
		}
		assert.Equal(c, "update", o.GetOperation())
		if assert.NotNil(c, o.GetTables()) {
			assert.Len(c, o.GetTables().GetEntries(), 2)
			assert.Contains(c, o.GetTables().GetEntries(), "entity-1")
			assert.Contains(c, o.GetTables().GetEntries(), "entity-2")
		}
	}, time.Second*20, time.Millisecond*10)
}

func (j *jwks) signJWT(t *testing.T, jwkPriv jwk.Key, id string) []byte {
	t.Helper()

	now := time.Now()

	token, err := jwt.NewBuilder().
		Audience([]string{"spiffe://localhost/ns/default/dapr-sentry"}).
		Expiration(now.Add(time.Hour)).
		IssuedAt(now).
		Subject(id).
		Build()
	require.NoError(t, err)

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.ES256, jwkPriv))
	require.NoError(t, err)

	return signed
}

func (j *jwks) genPrivateJWK(t *testing.T) (jwk.Key, []byte) {
	t.Helper()

	// Generate a signing key
	privK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	jwtSigningKeyPriv, err := jwk.FromRaw(privK)
	require.NoError(t, err)

	jwtSigningKeyPriv.Set("kid", "mykey")
	jwtSigningKeyPriv.Set("alg", "ES256")
	jwtSigningKeyPub, err := jwtSigningKeyPriv.PublicKey()
	require.NoError(t, err)

	jwtSigningKeyPubJSON, err := json.Marshal(jwtSigningKeyPub)
	require.NoError(t, err)

	return jwtSigningKeyPriv, jwtSigningKeyPubJSON
}
