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

package service

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/client/clientset/versioned/fake"
	"github.com/dapr/dapr/pkg/healthz"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
)

var des = serializer.NewCodecFactory(
	runtime.NewScheme(),
).UniversalDeserializer()

func init() {
	log.SetOutputLevel(logger.FatalLevel)
}

type MockWriter struct{}

func (m MockWriter) Header() http.Header {
	return http.Header{}
}

func (m MockWriter) Write(data []byte) (int, error) {
	return 0, nil
}

func (m MockWriter) WriteHeader(statusCode int) {
}

// This fuzz test sends randomized requests to handleRequest.
func FuzzHandleRequest(f *testing.F) {
	f.Fuzz(func(t *testing.T, body []byte) {
		ar1 := &v1.AdmissionReview{}
		ff := fuzz.NewConsumer(body)
		ff.GenerateStruct(ar1)
		if ar1.Request == nil {
			return
		}
		arBody, err := json.Marshal(ar1)
		if err != nil {
			return
		}
		ar := v1.AdmissionReview{}
		_, _, err = des.Decode(arBody, nil, &ar)
		if err != nil {
			return
		}
		if ar.Request == nil {
			return
		}
		r, err := http.NewRequest(http.MethodPost, "", bytes.NewReader(arBody))
		if err != nil {
			return
		}
		r.Header.Add("Content-Type", runtime.ContentTypeJSON)
		authID := "test-auth-id"
		i, err := NewInjector(Options{
			AuthUIDs: []string{authID},
			Config: Config{
				SidecarImage:                      "test-image",
				Namespace:                         "test-ns",
				ControlPlaneTrustDomain:           "test-trust-domain",
				AllowedServiceAccountsPrefixNames: "vc-proj*:sa-dev*,vc-all-allowed*:*",
			},
			DaprClient: fake.NewSimpleClientset(),
			KubeClient: kubernetesfake.NewSimpleClientset(),
			Healthz:    healthz.New(),
		})
		if err != nil {
			panic(err)
		}

		i.(*injector).handleRequest(MockWriter{}, r)
	})
}
