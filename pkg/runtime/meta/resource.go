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

package meta

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
)

// Resource interface that applies to both Component and HTTPEndpoint
// resources.
type Resource interface {
	Kind() string
	GetName() string
	GetNamespace() string
	LogName() string
	GetSecretStore() string
	NameValuePairs() []common.NameValuePair

	// Returns a deep copy of the resource, with the object meta set only with
	// Name and Namespace.
	EmptyMetaDeepCopy() metav1.Object
}
