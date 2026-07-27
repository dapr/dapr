/*
Copyright 2026 The Dapr Authors
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

package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/pubsub"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

func inlinePubsubComp(name string) componentsapi.Component {
	return componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: componentsapi.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: daprt.GetFakeMetadataItems(),
		},
	}
}

// TestInitInline exercises the synchronous, non-loop init/close path taken when
// Process is not running (Processor.running == false). This is the path
// initInline/closeInline serve; the loop-based path is covered elsewhere.
func TestInitInline(t *testing.T) {
	t.Run("commits a component when Process is not running", func(t *testing.T) {
		proc, reg := newTestProc()
		require.False(t, proc.running.Load(), "Process must not be running for the inline path")

		mockPubSub := new(daprt.MockPubSub)
		reg.PubSubs().RegisterComponent(
			func(logger.Logger) pubsub.PubSub { return mockPubSub },
			"mockPubSub",
		)
		mockPubSub.On("Init", mock.Anything).Return(nil)

		require.NoError(t, proc.Init(t.Context(), inlinePubsubComp("inline-pubsub")))

		_, ok := proc.compStore.GetComponent("inline-pubsub")
		assert.True(t, ok, "component should be committed to the store")
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
	})

	t.Run("incorrect type returns an error", func(t *testing.T) {
		proc, _ := newTestProc()
		err := proc.Init(t.Context(), componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "bad"},
			Spec:       componentsapi.ComponentSpec{Type: "pubsubs.mockPubSub", Version: "v1"},
		})
		require.Error(t, err)
		assert.Equal(t, "incorrect type pubsubs.mockPubSub", err.Error())
	})

	t.Run("component behind an unready secret store is deferred, not committed", func(t *testing.T) {
		proc, reg := newTestProc()
		mockPubSub := new(daprt.MockPubSub)
		reg.PubSubs().RegisterComponent(
			func(logger.Logger) pubsub.PubSub { return mockPubSub },
			"mockPubSub",
		)

		comp := inlinePubsubComp("deferred-pubsub")
		comp.SecretStore = "notready"
		comp.Spec.Metadata = append(comp.Spec.Metadata, commonapi.NameValuePair{
			Name:         "password",
			SecretKeyRef: commonapi.SecretKeyRef{Name: "mysecret", Key: "key1"},
		})

		// The inline path reports success but does not commit a component that is
		// parked behind a secret store that has not been loaded yet.
		require.NoError(t, proc.Init(t.Context(), comp))

		_, ok := proc.compStore.GetComponent("deferred-pubsub")
		assert.False(t, ok, "deferred component must not be committed")
		mockPubSub.AssertNotCalled(t, "Init", mock.Anything)
	})

	t.Run("closeInline closes and removes a committed component", func(t *testing.T) {
		proc, reg := newTestProc()
		mockPubSub := new(daprt.MockPubSub)
		reg.PubSubs().RegisterComponent(
			func(logger.Logger) pubsub.PubSub { return mockPubSub },
			"mockPubSub",
		)
		mockPubSub.On("Init", mock.Anything).Return(nil)
		mockPubSub.On("Close").Return(nil)

		comp := inlinePubsubComp("close-pubsub")
		require.NoError(t, proc.Init(t.Context(), comp))
		require.NoError(t, proc.Close(t.Context(), comp))

		_, ok := proc.compStore.GetComponent("close-pubsub")
		assert.False(t, ok, "component should be removed from the store after Close")
		mockPubSub.AssertNumberOfCalls(t, "Close", 1)
	})
}
