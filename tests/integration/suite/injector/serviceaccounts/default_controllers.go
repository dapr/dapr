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

package serviceaccounts

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
	suite.Register(new(defaultControllers))
}

// defaultControllers verifies that the hardcoded kube-system controller
// service accounts (AllowedServiceAccountInfos) remain authorized even when
// custom ALLOWED_SERVICE_ACCOUNTS are configured.
type defaultControllers struct {
	injector *procinjector.Injector
}

func (d *defaultControllers) Setup(t *testing.T) []framework.Option {
	sentry := procsentry.New(t,
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithNamespace("dapr-system"),
	)
	d.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(sentry),
		procinjector.WithAllowedServiceAccounts("custom-ns:custom-sa"),
	)
	return []framework.Option{
		framework.WithProcesses(sentry, d.injector),
	}
}

func (d *defaultControllers) Run(t *testing.T, ctx context.Context) {
	d.injector.WaitUntilRunning(t, ctx)

	review := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request: &admissionv1.AdmissionRequest{
			UID:       uuid.NewUUID(),
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Name:      "test-app",
			Namespace: "default",
			Operation: "CREATE",
			UserInfo: authenticationv1.UserInfo{
				Username: "system:serviceaccount:kube-system:deployment-controller",
			},
			Object: runtime.RawExtension{Raw: buildPod("test-app")},
		},
	}

	ar := d.injector.SendAdmission(t, ctx, review)
	require.NotNil(t, ar.Response)
	assert.True(t, ar.Response.Allowed)
	assert.NotEmpty(t, ar.Response.Patch, "default kube-system controller should still inject sidecar")
}
