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
	"time"

	"github.com/stretchr/testify/assert"
	admissionv1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/dapr/dapr/tests/integration/framework"
	procinjector "github.com/dapr/dapr/tests/integration/framework/process/injector"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(alreadyInjected))
}

// alreadyInjected verifies that a pod which already contains a daprd container
// is not double-injected on reinvocation.
type alreadyInjected struct {
	injector *procinjector.Injector
}

func (a *alreadyInjected) Setup(t *testing.T) []framework.Option {
	sentry := procsentry.New(t,
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithNamespace("dapr-system"),
	)
	a.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(sentry),
	)
	return []framework.Option{
		framework.WithProcesses(sentry, a.injector),
	}
}

func (a *alreadyInjected) Run(t *testing.T, ctx context.Context) {
	a.injector.WaitUntilRunning(t, ctx)

	podBytes := buildPodWithContainers("already-injected", map[string]string{
		"dapr.io/enabled":  "true",
		"dapr.io/app-id":   "already-injected",
		"dapr.io/app-port": "3000",
	}, []corev1.Container{
		{Name: "main", Image: "docker.io/app:latest"},
		{Name: "daprd", Image: "integration.dapr.io/dapr:latest"},
	})
	review := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request: &admissionv1.AdmissionRequest{
			UID:       uuid.NewUUID(),
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Name:      "already-injected",
			Namespace: "dapr-system",
			Operation: "CREATE",
			UserInfo:  authenticationv1.UserInfo{Groups: []string{"system:masters"}},
			Object:    runtime.RawExtension{Raw: podBytes},
		},
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		ar := a.injector.SendAdmission(t, ctx, review)
		if !assert.NotNil(c, ar.Response) {
			return
		}
		assert.True(c, ar.Response.Allowed)
		assert.Empty(c, ar.Response.Patch, "should not double-inject when daprd is present")
	}, time.Second*10, 100*time.Millisecond)
}
