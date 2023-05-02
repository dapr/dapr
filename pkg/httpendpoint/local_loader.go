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

package httpendpoint

import (
	httpEndpointsV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/components"
)

// LocalHTTPEndpoints loads http endpoints from a given directory.
type LocalHTTPEndpoints struct {
	httpEndpointsManifestLoader ManifestLoader[httpEndpointsV1alpha1.HTTPEndpoint]
}

// NewLocalHTTPEndpoints returns a new LocalHTTPEndpoint.
func NewLocalHTTPEndpoints(resourcesPaths ...string) *LocalHTTPEndpoints {
	return &LocalHTTPEndpoints{
		httpEndpointsManifestLoader: components.NewDiskManifestLoader[httpEndpointsV1alpha1.HTTPEndpoint](resourcesPaths...),
	}
}

// LocalHTTPEndpoints loads dapr http endpoints from a given directory.
func (s *LocalHTTPEndpoints) LoadHTTPEndpoints() ([]httpEndpointsV1alpha1.HTTPEndpoint, error) {
	return s.httpEndpointsManifestLoader.Load()
}
