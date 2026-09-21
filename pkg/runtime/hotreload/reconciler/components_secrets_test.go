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

package reconciler

import (
	"bytes"
	"encoding/base64"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/kit/logger"
)

// secretComp builds a pubsub component whose metadata carries a secretKeyRef
// alongside the value the operator already resolved, which is the shape a
// component reaches the sidecar in on Kubernetes: the secret bytes, base64
// encoded, then JSON marshalled as a string.
func secretComp(plaintext string) compapi.Component {
	encoded := strconv.Quote(base64.StdEncoding.EncodeToString([]byte(plaintext)))
	return compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "spubsub"},
		Spec: compapi.ComponentSpec{
			Type:    "pubsub.mockPubSub",
			Version: "v1",
			Metadata: []commonapi.NameValuePair{{
				Name:         "brokers",
				SecretKeyRef: commonapi.SecretKeyRef{Name: "comp-sec-1", Key: "brokers"},
				Value:        commonapi.DynamicValue{JSON: apiextv1.JSON{Raw: []byte(encoded)}},
			}},
		},
	}
}

// captureSecretLog redirects the secret processor's logger into a buffer for
// the duration of the test. update blocks on the processor loop, so the test
// goroutine only reads the buffer after the processor is done writing to it.
func captureSecretLog(t *testing.T) *bytes.Buffer {
	t.Helper()

	buf := new(bytes.Buffer)
	l := logger.NewLogger("dapr.runtime.processor.secret")
	l.SetOutput(buf)
	t.Cleanup(func() { l.SetOutput(os.Stdout) })

	return buf
}

// A component's secrets must be resolved exactly once on its way into the
// processor. Spec.Metadata is a slice, so resolving secrets on the component
// update was handed writes through the backing array the caller shares, and
// the processor resolves again on init. In Kubernetes that second pass base64
// decodes values which are no longer encoded, so every one of them fails and
// is logged with the first byte of the secret quoted.
func Test_components_update_resolvesSecretsOnce(t *testing.T) {
	const (
		first  = "https://broker.example.com:9096"
		second = "https://broker.example.com:9097"
	)

	t.Run("new component", func(t *testing.T) {
		m, proc, cs, _ := newComponentsManager(t, modes.KubernetesMode)
		ctx := runProc(t, proc)
		secretLog := captureSecretLog(t)

		require.NoError(t, m.update(ctx, secretComp(first)))

		installed, ok := cs.GetComponent("spubsub")
		require.True(t, ok)
		assert.Equal(t, first, string(installed.Spec.Metadata[0].Value.Raw))
		assert.NotContains(t, secretLog.String(), "Error decoding secret")
	})

	// The same holds when the component is already installed, which is the
	// path that has to resolve a copy in order to compare against the stored
	// version.
	t.Run("reload", func(t *testing.T) {
		m, proc, cs, _ := newComponentsManager(t, modes.KubernetesMode)
		ctx := runProc(t, proc)

		require.NoError(t, m.update(ctx, secretComp(first)))

		secretLog := captureSecretLog(t)
		require.NoError(t, m.update(ctx, secretComp(second)))

		installed, ok := cs.GetComponent("spubsub")
		require.True(t, ok)
		assert.Equal(t, second, string(installed.Spec.Metadata[0].Value.Raw))
		assert.NotContains(t, secretLog.String(), "Error decoding secret")
	})

	// A repeated event for an unchanged component must still be recognised as
	// unchanged, which is what the comparison copy is resolved for. The
	// component is only observably skipped through the mock: a reload would
	// close and re-init it.
	t.Run("unchanged component is skipped", func(t *testing.T) {
		m, proc, cs, mockPubSub := newComponentsManager(t, modes.KubernetesMode)
		ctx := runProc(t, proc)

		require.NoError(t, m.update(ctx, secretComp(first)))
		installed, ok := cs.GetComponent("spubsub")
		require.True(t, ok)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		require.NoError(t, m.update(ctx, secretComp(first)))

		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Close", 0)

		reinstalled, ok := cs.GetComponent("spubsub")
		require.True(t, ok)
		assert.Equal(t, installed, reinstalled)
		assert.Equal(t, first, string(reinstalled.Spec.Metadata[0].Value.Raw))
	})
}
