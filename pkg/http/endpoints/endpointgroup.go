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

package endpoints

import (
	"net/http"
)

// EndpointGroup is a group of endpoints.
type EndpointGroup struct {
	// Endpoint group name.
	Name EndpointGroupName
	// Endpoint group version.
	Version EndpointGroupVersion
	// AppendSpanAttributes is a function invoked by the tracing middleware.
	// It receives a map where the callback can add attributes to be added to the span.
	// If this is not defined, no additional data is added to the span.
	AppendSpanAttributes AppendSpanAttributesFn
	// MethodName is an optional method that returns the "method" key used in the server monitoring metrics.
	// If unset, the default value is to use the endpoint name.
	MethodName MethodNameFn
}

type (
	AppendSpanAttributesFn = func(r *http.Request, m map[string]string)
	MethodNameFn           = func(r *http.Request) string
)

// EndpointGroupName is the name of an endpoint group.
type EndpointGroupName string

const (
	EndpointGroupServiceInvocation EndpointGroupName = "invoke"
	EndpointGroupState             EndpointGroupName = "state"
	EndpointGroupPubsub            EndpointGroupName = "publish"
	EndpointGroupBindings          EndpointGroupName = "bindings"
	EndpointGroupSecrets           EndpointGroupName = "secrets"
	EndpointGroupActors            EndpointGroupName = "actors"
	EndpointGroupMetadata          EndpointGroupName = "metadata"
	EndpointGroupConfiguration     EndpointGroupName = "configuration"
	EndpointGroupLock              EndpointGroupName = "lock"
	EndpointGroupUnlock            EndpointGroupName = "unlock"
	EndpointGroupCrypto            EndpointGroupName = "crypto"
	EndpointGroupSubtleCrypto      EndpointGroupName = "subtlecrypto"
	EndpointGroupWorkflow          EndpointGroupName = "workflows"
	EndpointGroupHealth            EndpointGroupName = "healthz"
	EndpointGroupShutdown          EndpointGroupName = "shutdown"
)

// EndpointGroupVersion is the version of an endpoint group.
type EndpointGroupVersion string

const (
	EndpointGroupVersion1       EndpointGroupVersion = "v1"       // Alias: v1.0
	EndpointGroupVersion1alpha1 EndpointGroupVersion = "v1alpha1" // Alias: v1.0-alpha1
	EndpointGroupVersion1beta1  EndpointGroupVersion = "v1beta1"  // Alias: v1.0-beta1
)
