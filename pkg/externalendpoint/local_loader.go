/*
Copyright 2021 The Dapr Authors
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

package externalendpoint

import (
	externalendpointsV1alpha1 "github.com/dapr/dapr/pkg/apis/externalHTTPEndpoint/v1alpha1"
)

// LocalExternalHTTPEndpoints loads external http endpoints from a given directory.
type LocalExternalHTTPEndpoints struct {
	externalHTTPEndpointsManifestLoader ManifestLoader[externalendpointsV1alpha1.ExternalHTTPEndpoint]
}

// NewLocalExternalHTTPEndpoints returns a new LocalExternalHTTPEndpoint.
func NewLocalExternalHTTPEndpoints(resourcesPaths ...string) *LocalExternalHTTPEndpoints {
	return &LocalExternalHTTPEndpoints{
		externalHTTPEndpointsManifestLoader: NewDiskManifestLoader[externalendpointsV1alpha1.ExternalHTTPEndpoint](resourcesPaths...),
	}
}

// LocalExternalHTTPEndpoints loads dapr external http endpoints from a given directory.
func (s *LocalExternalHTTPEndpoints) LoadExternalHTTPEndpoints() ([]externalendpointsV1alpha1.ExternalHTTPEndpoint, error) {
	return s.externalHTTPEndpointsManifestLoader.Load()
}
