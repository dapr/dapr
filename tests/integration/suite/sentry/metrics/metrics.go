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

package metrics

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/tests/integration/framework"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(expiry))
}

// expiry tests the certificate expiry metric.
type expiry struct {
	notGiven *procsentry.Sentry
	given    *procsentry.Sentry
}

func (e *expiry) Setup(t *testing.T) []framework.Option {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	onemonth := time.Hour * 24 * 30
	bundle, err := ca.GenerateBundle(rootKey, "integration.test.dapr.io", time.Second*5, &onemonth)
	require.NoError(t, err)

	e.notGiven = procsentry.New(t, procsentry.WithDontGiveBundle(true))
	e.given = procsentry.New(t, procsentry.WithCABundle(bundle))

	return []framework.Option{
		framework.WithProcesses(e.notGiven, e.given),
	}
}

func (e *expiry) Run(t *testing.T, ctx context.Context) {
	e.notGiven.WaitUntilRunning(t, ctx)
	e.given.WaitUntilRunning(t, ctx)

	time.Sleep(time.Second)
	client := util.HTTPClient(t)

	testExpiry := func(name string, proc *procsentry.Sentry, expTime time.Time) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/metrics", proc.MetricsPort()), nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)

		respBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.NoError(t, resp.Body.Close())

		t.Run(name+": test `dapr_sentry_issuercert_expiry_timestamp` metric is present with correct date", func(t *testing.T) {
			for _, line := range bytes.Split(respBody, []byte("\n")) {
				if len(line) == 0 || line[0] == '#' {
					continue
				}

				split := bytes.Split(line, []byte(" "))
				if len(split) != 2 {
					continue
				}

				if string(split[0]) != "dapr_sentry_issuercert_expiry_timestamp" {
					continue
				}

				timestamp, err := strconv.ParseFloat(string(split[1]), 64)
				require.NoError(t, err)

				tsTime := time.Unix(int64(timestamp), 0)
				assert.InDelta(t, expTime.Unix(), tsTime.Unix(), 20)
				return
			}
			assert.Fail(t, "metric not found")
		})
	}

	testExpiry("certificate not given", e.notGiven, time.Now().Add(time.Hour*24*365))
	testExpiry("certificate given", e.given, time.Now().Add(time.Hour*24*30))
}
