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

package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
)

func Test_component(t *testing.T) {
	var store Store[componentsapi.Component]
	compStore := compstore.New()
	store = NewComponent(compStore)
	comp1, comp2 := componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "1"},
	}, componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: "2"},
	}

	require.NoError(t, compStore.AddPendingComponentForCommit(comp1))
	require.NoError(t, compStore.CommitPendingComponent())
	require.NoError(t, compStore.AddPendingComponentForCommit(comp2))
	require.NoError(t, compStore.CommitPendingComponent())
	assert.ElementsMatch(t, []componentsapi.Component{comp1, comp2}, store.List())

	compStore.DeleteComponent("1")
	assert.ElementsMatch(t, []componentsapi.Component{comp2}, store.List())

	compStore.DeleteComponent("2")
	assert.ElementsMatch(t, []componentsapi.Component{}, store.List())
}
