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

package authorizer

import (
	"reflect"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.authorizer")

// Type of function that determines if a component is authorized.
// The function receives the component and must return true if the component is authorized.
type ComponentAuthorizer func(component componentsapi.Component) bool

// Type of function that determines if an http endpoint is authorized.
// The function receives the http endpoint and must return true if the http endpoint is authorized.
type HTTPEndpointAuthorizer func(endpoint httpendpointsapi.HTTPEndpoint) bool

type Options struct {
	ID           string
	Namespace    string
	GlobalConfig *config.Configuration
}

type Authorizer struct {
	id        string
	namespace string

	componentAuthorizers    []ComponentAuthorizer
	httpEndpointAuthorizers []HTTPEndpointAuthorizer
}

func New(opts Options) *Authorizer {
	r := &Authorizer{
		id:        opts.ID,
		namespace: opts.Namespace,
	}

	r.componentAuthorizers = []ComponentAuthorizer{r.namespaceComponentAuthorizer}
	if opts.GlobalConfig != nil && opts.GlobalConfig.Spec.ComponentsSpec != nil && len(opts.GlobalConfig.Spec.ComponentsSpec.Deny) > 0 {
		dl := newComponentDenyList(opts.GlobalConfig.Spec.ComponentsSpec.Deny)
		r.componentAuthorizers = append(r.componentAuthorizers, dl.IsAllowed)
	}

	r.httpEndpointAuthorizers = []HTTPEndpointAuthorizer{r.namespaceHTTPEndpointAuthorizer}

	return r
}

func (a *Authorizer) GetAuthorizedObjects(objects any, authorizer func(any) bool) any {
	reflectValue := reflect.ValueOf(objects)
	authorized := reflect.MakeSlice(reflectValue.Type(), 0, reflectValue.Len())
	for i := 0; i < reflectValue.Len(); i++ {
		object := reflectValue.Index(i).Interface()
		if authorizer(object) {
			authorized = reflect.Append(authorized, reflect.ValueOf(object))
		}
	}
	return authorized.Interface()
}

func (a *Authorizer) IsObjectAuthorized(object any) bool {
	switch obj := object.(type) {
	case httpendpointsapi.HTTPEndpoint:
		for _, auth := range a.httpEndpointAuthorizers {
			if !auth(obj) {
				return false
			}
		}
	case componentsapi.Component:
		for _, auth := range a.componentAuthorizers {
			if !auth(obj) {
				return false
			}
		}
	}
	return true
}

func (a *Authorizer) namespaceHTTPEndpointAuthorizer(endpoint httpendpointsapi.HTTPEndpoint) bool {
	switch {
	case a.namespace == "",
		endpoint.ObjectMeta.Namespace == "",
		(a.namespace != "" && endpoint.ObjectMeta.Namespace == a.namespace):
		return endpoint.IsAppScoped(a.id)
	default:
		return false
	}
}

func (a *Authorizer) namespaceComponentAuthorizer(comp componentsapi.Component) bool {
	if a.namespace == "" || comp.ObjectMeta.Namespace == "" || (a.namespace != "" && comp.ObjectMeta.Namespace == a.namespace) {
		return comp.IsAppScoped(a.id)
	}

	return false
}
