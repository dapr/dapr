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
	"context"
	"testing"

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
	suite.Register(new(daprDisabled))
}

// daprDisabled verifies that a pod with dapr.io/enabled explicitly set to
// "false" is silently allowed even when the requesting service account is not
// whitelisted.
type daprDisabled struct {
	injector *procinjector.Injector
}

func (d *daprDisabled) Setup(t *testing.T) []framework.Option {
	sentry := procsentry.New(t,
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithNamespace("dapr-system"),
	)
	d.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(sentry),
	)
	return []framework.Option{
		framework.WithProcesses(sentry, d.injector),
	}
}

func (d *daprDisabled) Run(t *testing.T, ctx context.Context) {
	d.injector.WaitUntilRunning(t, ctx)

	podBytes := buildPod("disabled-app", map[string]string{
		"dapr.io/enabled": "false",
	})
	review := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request: &admissionv1.AdmissionRequest{
			UID:       uuid.NewUUID(),
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Name:      "disabled-app",
			Namespace: "default",
			Operation: "CREATE",
			UserInfo: authenticationv1.UserInfo{
				Username: "system:serviceaccount:some-ns:some-sa",
			},
			Object: runtime.RawExtension{Raw: podBytes},
		},
	}

	ar := d.injector.SendAdmission(t, ctx, review)
	require.NotNil(t, ar.Response)
	assert.True(t, ar.Response.Allowed, "dapr-disabled pod should be allowed")
	assert.Empty(t, ar.Response.Patch, "dapr-disabled pod should not be patched")
}
