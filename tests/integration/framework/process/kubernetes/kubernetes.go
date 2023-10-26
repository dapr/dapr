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

package kubernetes

import (
	"context"
	"net/http"
	"testing"

	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
)

// Option is a function that configures the mock Kubernetes process.
type Option func(*options)

// Kubernetes is a mock Kubernetes API server process.
type Kubernetes struct {
	http *prochttp.HTTP
}

func New(t *testing.T, fopts ...Option) *Kubernetes {
	t.Helper()

	var opts options
	for _, fopt := range fopts {
		fopt(&opts)
	}

	handler := http.NewServeMux()

	handler.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json;g=apidiscovery.k8s.io;v=v2beta1;as=APIGroupDiscoveryList")
		w.Write([]byte(apiDiscovery))
	})

	handler.HandleFunc("/apis", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "application/json;g=apidiscovery.k8s.io;v=v2beta1;as=APIGroupDiscoveryList")
		w.Write([]byte(apisDiscovery))
	})

	for _, h := range opts.handlers {
		handler.HandleFunc(h.path, h.handler)
	}

	return &Kubernetes{
		http: prochttp.New(t, prochttp.WithHandler(handler)),
	}
}

func (k *Kubernetes) Port() int {
	return k.http.Port()
}

func (k *Kubernetes) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	k.http.Run(t, ctx)
}

func (k *Kubernetes) Cleanup(t *testing.T) {
	t.Helper()
	k.http.Cleanup(t)
}
