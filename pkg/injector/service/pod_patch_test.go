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

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	clientfake "github.com/dapr/dapr/pkg/client/clientset/versioned/fake"
	"github.com/dapr/kit/ptr"
)

func Test_mtlsEnabled(t *testing.T) {
	t.Run("if configuration doesn't exist, return true", func(t *testing.T) {
		cl := clientfake.NewSimpleClientset()
		assert.True(t, mTLSEnabled("test-ns", cl))
	})

	t.Run("if configuration exists and is false, return false", func(t *testing.T) {
		cl := clientfake.NewSimpleClientset()
		cl.ConfigurationV1alpha1().Configurations("test-ns").Create(
			&configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "daprsystem",
					Namespace: "test-ns",
				},
				Spec: configapi.ConfigurationSpec{MTLSSpec: &configapi.MTLSSpec{Enabled: ptr.Of(false)}},
			},
		)
		assert.False(t, mTLSEnabled("test-ns", cl))
	})

	t.Run("if configuration exists and is true, return true", func(t *testing.T) {
		cl := clientfake.NewSimpleClientset()
		cl.ConfigurationV1alpha1().Configurations("test-ns").Create(
			&configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "daprsystem",
					Namespace: "test-ns",
				},
				Spec: configapi.ConfigurationSpec{MTLSSpec: &configapi.MTLSSpec{Enabled: ptr.Of(true)}},
			},
		)
		assert.True(t, mTLSEnabled("test-ns", cl))
	})

	t.Run("if configuration exists and is nil, return true", func(t *testing.T) {
		cl := clientfake.NewSimpleClientset()
		cl.ConfigurationV1alpha1().Configurations("test-ns").Create(
			&configapi.Configuration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "daprsystem",
					Namespace: "test-ns",
				},
				Spec: configapi.ConfigurationSpec{MTLSSpec: &configapi.MTLSSpec{Enabled: nil}},
			},
		)
		assert.True(t, mTLSEnabled("test-ns", cl))
	})
}
