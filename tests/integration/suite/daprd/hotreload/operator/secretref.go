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

package operator

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(secretref))
}

// secretref hot reloads a component whose metadata carries a secretKeyRef. On
// Kubernetes the operator has already resolved that reference, handing daprd
// the secret base64 encoded, and daprd decodes it on its way into the
// component. That decode must happen exactly once per reload: a second pass
// over an already decoded value fails to parse and logs the error, quoting the
// first byte of the secret.
type secretref struct {
	daprd    *daprd.Daprd
	operator *operator.Operator
	logline  *logline.LogLine
	client   *http.Client
}

func (s *secretref) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	s.operator = operator.New(t,
		operator.WithSentry(sentry),
	)

	s.logline = logline.New(t, logline.WithCaptureAll())

	s.client = client.HTTP(t)

	s.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(s.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
		daprd.WithLogLineStdout(s.logline),
		daprd.WithExecOptions(
			exec.WithStderr(s.logline.Stderr()),
			exec.WithEnvVars(t,
				"DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors),
				"FOO_SEC_1", "bar1",
				"BAR_SEC_1", "baz1",
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, s.operator, s.logline, s.daprd),
	}
}

func (s *secretref) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)

	require.Empty(t, s.daprd.GetMetaRegisteredComponents(t, ctx))

	t.Run("a created component resolves its secret ref exactly once", func(t *testing.T) {
		s.logline.Reset()

		comp := s.comp("FOO_")
		s.operator.SetComponents(comp)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Len(c, s.daprd.GetMetaRegisteredComponents(t, ctx), 1)
		}, time.Second*5, time.Millisecond*10)

		s.read(t, ctx, "SEC_1", "bar1")
		s.assertNoDecodeError(t)
	})

	t.Run("an updated component resolves its secret ref exactly once", func(t *testing.T) {
		s.logline.Reset()

		comp := s.comp("BAR_")
		s.operator.SetComponents(comp)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, "baz1", s.get(t, ctx, "SEC_1"))
		}, time.Second*5, time.Millisecond*10)

		s.assertNoDecodeError(t)
	})

	t.Run("an unchanged component is not reloaded", func(t *testing.T) {
		s.logline.Reset()

		comp := s.comp("BAR_")
		s.operator.SetComponents(comp)
		s.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &comp, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, s.logline.Contains("Component update skipped: no changes detected"))
		}, time.Second*5, time.Millisecond*10)

		s.read(t, ctx, "SEC_1", "baz1")
		s.assertNoDecodeError(t)
	})
}

// comp builds the local env secret store, giving its prefix through a
// secretKeyRef whose value is already populated the way the operator populates
// it: the secret bytes, base64 encoded, then JSON marshalled as a string.
func (s *secretref) comp(prefix string) compapi.Component {
	encoded := strconv.Quote(base64.StdEncoding.EncodeToString([]byte(prefix)))
	return compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "mysecrets"},
		Spec: compapi.ComponentSpec{
			Type:    "secretstores.local.env",
			Version: "v1",
			Metadata: []common.NameValuePair{{
				Name:         "PREFIX",
				SecretKeyRef: common.SecretKeyRef{Name: "env-prefix", Key: "prefix"},
				Value:        common.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(encoded)}},
			}},
		},
	}
}

// assertNoDecodeError fails if daprd logged a secret decode error, which is
// what a second decode of an already decoded value produces. The error is
// logged while the component is being processed, so it has already been
// written by the time the component is observable over the API.
func (s *secretref) assertNoDecodeError(t *testing.T) {
	t.Helper()
	assert.False(t, s.logline.Contains("Error decoding secret"), "secret was decoded more than once")
}

func (s *secretref) read(t *testing.T, ctx context.Context, key, expValue string) {
	t.Helper()
	assert.Equal(t, expValue, s.get(t, ctx, key))
}

// get returns the value the secret store gives for key, which reflects the
// prefix the component was configured with, or an empty string if the read did
// not succeed. Failures are recorded rather than fatal so that it can also be
// polled while the component is being reloaded.
func (s *secretref) get(t assert.TestingT, ctx context.Context, key string) string {
	getURL := fmt.Sprintf("http://localhost:%d/v1.0/secrets/mysecrets/%s", s.daprd.HTTPPort(), url.QueryEscape(key))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
	if !assert.NoError(t, err) {
		return ""
	}

	resp, err := s.client.Do(req)
	if !assert.NoError(t, err) {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, resp.Body.Close())
	if !assert.NoError(t, err) {
		return ""
	}
	if !assert.Equal(t, http.StatusOK, resp.StatusCode, string(body)) {
		return ""
	}

	var m map[string]string
	if !assert.NoError(t, json.Unmarshal(body, &m)) {
		return ""
	}

	return m[key]
}
