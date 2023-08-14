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

package compstore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

func Test_component(t *testing.T) {
	var comp ComponentStore[compapi.Component]
	store := compstore.New()
	comp = NewComponent(store)
	comp1, comp2 := compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "1"},
	}, compapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "2"},
	}

	store.AddComponent(comp1)
	store.AddComponent(comp2)
	assert.ElementsMatch(t, []compapi.Component{comp1, comp2}, comp.List())

	store.DeleteComponent("1")
	assert.ElementsMatch(t, []compapi.Component{comp2}, comp.List())

	store.DeleteComponent("2")
	assert.ElementsMatch(t, []compapi.Component{}, comp.List())
}

func Test_endpoint(t *testing.T) {
	var endpoint ComponentStore[httpendapi.HTTPEndpoint]
	store := compstore.New()
	endpoint = NewHTTPEndpoint(store)
	endpoint1, endpoint2 := httpendapi.HTTPEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "1"},
	}, httpendapi.HTTPEndpoint{
		ObjectMeta: metav1.ObjectMeta{Name: "2"},
	}

	store.AddHTTPEndpoint(endpoint1)
	store.AddHTTPEndpoint(endpoint2)
	assert.ElementsMatch(t, []httpendapi.HTTPEndpoint{endpoint1, endpoint2}, endpoint.List())

	store.DeleteHTTPEndpoint("1")
	assert.ElementsMatch(t, []httpendapi.HTTPEndpoint{endpoint2}, endpoint.List())

	store.DeleteHTTPEndpoint("2")
	assert.ElementsMatch(t, []httpendapi.HTTPEndpoint{}, endpoint.List())
}
