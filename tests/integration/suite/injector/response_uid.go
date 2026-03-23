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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"

	"github.com/dapr/dapr/tests/integration/framework"
	procinjector "github.com/dapr/dapr/tests/integration/framework/process/injector"
	procsentry "github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(responseUID))
}

// responseUID verifies that the admission response UID matches the request UID.
type responseUID struct {
	injector *procinjector.Injector
}

func (r *responseUID) Setup(t *testing.T) []framework.Option {
	sentry := procsentry.New(t,
		procsentry.WithTrustDomain("integration.test.dapr.io"),
		procsentry.WithNamespace("dapr-system"),
	)
	r.injector = procinjector.New(t,
		procinjector.WithNamespace("dapr-system"),
		procinjector.WithSentry(sentry),
	)
	return []framework.Option{
		framework.WithProcesses(sentry, r.injector),
	}
}

func (r *responseUID) Run(t *testing.T, ctx context.Context) {
	r.injector.WaitUntilRunning(t, ctx)

	podBytes := buildPod("uid-test", map[string]string{
		"dapr.io/enabled": "true",
		"dapr.io/app-id":  "uid-test",
	})
	requestUID := uuid.NewUUID()
	review := admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{Kind: "AdmissionReview", APIVersion: "admission.k8s.io/v1"},
		Request: &admissionv1.AdmissionRequest{
			UID:       requestUID,
			Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			Name:      "uid-test",
			Namespace: "dapr-system",
			Operation: "CREATE",
			UserInfo:  authenticationv1.UserInfo{Groups: []string{"system:masters"}},
			Object:    runtime.RawExtension{Raw: podBytes},
		},
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		ar := r.injector.SendAdmission(t, ctx, review)
		if !assert.NotNil(c, ar.Response) {
			return
		}
		assert.Equal(c, requestUID, ar.Response.UID, "response UID should match request UID")
	}, time.Second*10, 100*time.Millisecond)
}
