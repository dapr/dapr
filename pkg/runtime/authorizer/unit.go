//go:build unit
// +build unit

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

func (a *Authorizer) WithComponentAuthorizers(authorizers []ComponentAuthorizer) *Authorizer {
	a.componentAuthorizers = authorizers
	return a
}

func (a *Authorizer) WithAppendComponentAuthorizers(authorizers ...ComponentAuthorizer) *Authorizer {
	a.componentAuthorizers = append(a.componentAuthorizers, authorizers...)
	return a
}

func (a *Authorizer) WithHTTPEndpointAuthorizers(authorizers []HTTPEndpointAuthorizer) *Authorizer {
	a.httpEndpointAuthorizers = authorizers
	return a
}

func (a *Authorizer) WithAppendHTTPEndpointAuthorizers(authorizers ...HTTPEndpointAuthorizer) *Authorizer {
	a.httpEndpointAuthorizers = append(a.httpEndpointAuthorizers, authorizers...)
	return a
}
