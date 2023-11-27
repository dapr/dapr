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

package apis

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
)

type GenericNameValueResource struct {
	Name         string
	Namespace    string
	SecretStore  string
	ResourceKind string
	Pairs        []common.NameValuePair
}

func (g GenericNameValueResource) Kind() string {
	return g.ResourceKind
}

func (g GenericNameValueResource) GetName() string {
	return g.Name
}

func (g GenericNameValueResource) GetNamespace() string {
	return g.Namespace
}

func (g GenericNameValueResource) GetSecretStore() string {
	return g.SecretStore
}

func (g GenericNameValueResource) NameValuePairs() []common.NameValuePair {
	return g.Pairs
}

func (g GenericNameValueResource) LogName() string {
	return fmt.Sprintf("%s (%s)", g.Name, g.ResourceKind)
}

func (g GenericNameValueResource) EmptyMetaDeepCopy() metav1.Object {
	return &metav1.ObjectMeta{Name: g.Name}
}
