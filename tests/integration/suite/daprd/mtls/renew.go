/*
Copyright 2024 The Dapr Authors
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

package mtls

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(renew))
}

type renew struct {
	daprd         *daprd.Daprd
	sentry        *sentry.Sentry
	lock          sync.Mutex
	renewDuration time.Duration
	renewCalled   atomic.Int64
}

func (r *renew) Setup(t *testing.T) []framework.Option {
	r.sentry = sentry.New(t, sentry.WithSignCertificateFn(
		func(_ context.Context, req *sentryv1pb.SignCertificateRequest) (*sentryv1pb.SignCertificateResponse, error) {
			r.lock.Lock()
			defer r.lock.Unlock()

			der, _ := pem.Decode(req.GetCertificateSigningRequest())
			require.NotNil(t, der)
			csr, err := x509.ParseCertificateRequest(der.Bytes)
			require.NoError(t, err)

			cert := x509.Certificate{
				SerialNumber: big.NewInt(1),
				PublicKey:    csr.PublicKey,
				NotBefore:    time.Now(),
				NotAfter:     time.Now().Add(r.renewDuration),
				URIs: []*url.URL{
					spiffeid.RequireFromString("spiffe://localhost/ns/default/" + r.daprd.AppID()).URL(),
				},
			}

			signed, err := x509.CreateCertificate(rand.Reader, &cert, r.sentry.Bundle().X509.IssChain[0], csr.PublicKey, r.sentry.Bundle().X509.IssKey)
			require.NoError(t, err)
			signedPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: signed})

			r.renewCalled.Add(1)

			return &sentryv1pb.SignCertificateResponse{
				WorkloadCertificate: append(signedPEM, r.sentry.Bundle().X509.IssChainPEM...),
				ValidUntil:          timestamppb.New(time.Now().Add(r.renewDuration)),
			}, nil
		},
	))

	r.daprd = daprd.New(t,
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(r.sentry.Bundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(r.sentry.Address(t)),
		daprd.WithEnableMTLS(true),
	)

	r.renewDuration = time.Millisecond * 10

	return []framework.Option{
		framework.WithProcesses(r.sentry, r.daprd),
	}
}

func (r *renew) Run(t *testing.T, ctx context.Context) {
	assert.Eventually(t, func() bool {
		return r.renewCalled.Load() > 5
	}, time.Second*5, time.Millisecond*10)

	r.lock.Lock()
	called := r.renewCalled.Load()
	r.renewDuration = time.Hour * 24
	r.lock.Unlock()

	assert.Eventually(t, func() bool {
		return r.renewCalled.Load() > called
	}, time.Second*5, time.Millisecond*10)

	assert.Equal(t, called+1, r.renewCalled.Load())
	time.Sleep(time.Millisecond * 100)
	assert.Equal(t, called+1, r.renewCalled.Load())
}
