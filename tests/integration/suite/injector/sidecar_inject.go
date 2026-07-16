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

package injector

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	jsonpatch "github.com/evanphx/json-patch/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/dapr/dapr/tests/integration/framework"
	procinjector "github.com/dapr/dapr/tests/integration/framework/process/injector"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(sidecarInject))
}

// sidecarInject verifies that a Dapr-enabled pod is injected with a daprd
// sidecar container when submitted by an authorized user.
type sidecarInject struct {
	injector *procinjector.Injector
}

func (s *sidecarInject) Setup(t *testing.T) []framework.Option {
	sentry := procsentry.New(t,
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithNamespace("dapr-system"),
	)
	s.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(sentry),
	)
	return []framework.Option{
		framework.WithProcesses(sentry, s.injector),
	}
}

func (s *sidecarInject) Run(t *testing.T, ctx context.Context) {
	s.injector.WaitUntilRunning(t, ctx)

	podBytes := buildPod("dapr-app", map[string]string{
		"dapr.io/enabled":  "true",
		"dapr.io/app-id":   "dapr-app",
		"dapr.io/app-port": "3000",
	})
	review := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request: &admissionv1.AdmissionRequest{
			UID:       uuid.NewUUID(),
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Name:      "dapr-app",
			Namespace: "dapr-system",
			Operation: "CREATE",
			UserInfo:  authenticationv1.UserInfo{Groups: []string{"system:masters"}},
			Object:    runtime.RawExtension{Raw: podBytes},
		},
	}

	ar := s.injector.SendAdmission(t, ctx, review)
	require.NotNil(t, ar.Response)
	assert.True(t, ar.Response.Allowed)
	require.NotEmpty(t, ar.Response.Patch, "should contain sidecar patch")
	var ops jsonpatch.Patch
	require.NoError(t, json.Unmarshal(ar.Response.Patch, &ops))
	found := false
	for _, op := range ops {
		b, _ := json.Marshal(op)
		if bytes.Contains(b, []byte(`"daprd"`)) {
			found = true
			break
		}
	}
	assert.True(t, found, "patch should add daprd container")
}
