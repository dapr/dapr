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

package renewal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(restart))
}

// restart tests that a sentry restarted mid renewal-pending resumes the
// pending state from the stored credentials: it does not renew again, and
// keeps signing with the old issuer.
type restart struct {
	sentry *procsentry.Sentry
	bundle bundle.Bundle
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	// Renewal fires ~5s after start; the long grace keeps the pending state
	// stable across the restart.
	r.bundle = genBundle(t, "localhost", time.Second*65)

	r.sentry = procsentry.New(t,
		procsentry.WithConfiguration(configuration),
		procsentry.WithCABundle(r.bundle),
		procsentry.WithCATTL(time.Hour*24*365),
		procsentry.WithCARenewalThreshold(0.15),
		procsentry.WithPropagationGrace(time.Second*55),
	)

	return []framework.Option{
		framework.WithProcesses(r.sentry),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.sentry.WaitUntilRunning(t, ctx)

	credDir := r.sentry.CredentialsDir()
	caPath := filepath.Join(credDir, "ca.crt")
	nextCertPath := filepath.Join(credDir, "issuer.next.crt")

	// Wait for the renewal to land.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		anchorsPEM, err := os.ReadFile(caPath)
		if !assert.NoError(c, err) {
			return
		}
		assert.Len(c, parseAnchors(c, anchorsPEM), 2)
		assert.FileExists(c, nextCertPath)
	}, time.Second*30, time.Millisecond*100)

	anchorsBefore, err := os.ReadFile(caPath)
	require.NoError(t, err)
	nextBefore, err := os.ReadFile(nextCertPath)
	require.NoError(t, err)

	// Kill sentry and start a fresh process on the same credentials
	// directory.
	r.sentry.Cleanup(t)

	sentry2 := procsentry.New(t,
		procsentry.WithConfiguration(configuration),
		procsentry.WithCredentialsDir(credDir),
		procsentry.WithCABundle(r.bundle),
		procsentry.WithWriteTrustBundle(false),
		procsentry.WithCATTL(time.Hour*24*365),
		procsentry.WithCARenewalThreshold(0.15),
		procsentry.WithPropagationGrace(time.Second*55),
	)
	sentry2.Run(t, ctx)
	t.Cleanup(func() { sentry2.Cleanup(t) })
	sentry2.WaitUntilRunning(t, ctx)

	// Give the restarted sentry a moment to have acted, then assert the
	// pending state was resumed rather than renewed again.
	time.Sleep(time.Second * 2)

	anchorsAfter, err := os.ReadFile(caPath)
	require.NoError(t, err)
	assert.Equal(t, string(anchorsBefore), string(anchorsAfter), "no additional anchor must have been appended")
	assert.Len(t, parseAnchors(t, anchorsAfter), 2)

	nextAfter, err := os.ReadFile(nextCertPath)
	require.NoError(t, err)
	assert.Equal(t, string(nextBefore), string(nextAfter), "pending issuer unchanged")

	issuerPEM, err := os.ReadFile(filepath.Join(credDir, "issuer.crt"))
	require.NoError(t, err)
	assert.Equal(t, string(r.bundle.X509.IssChainPEM), string(issuerPEM), "still signing with the old issuer")
}
