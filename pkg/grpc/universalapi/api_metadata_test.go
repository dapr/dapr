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

package universalapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/actors"
	componentsV1alpha "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/expr"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
)

func TestGetMetadata(t *testing.T) {
	fakeComponent := componentsV1alpha.Component{}
	fakeComponent.Name = "testComponent"

	mockActors := new(actors.MockActors)
	mockActors.On("GetActiveActorsCount").Return(&runtimev1pb.ActiveActorsCount{
		Count: 10,
		Type:  "abcd",
	})

	fakeAPI := &UniversalAPI{
		AppID:  "fakeAPI",
		Actors: mockActors,
		GetComponentsFn: func() []componentsV1alpha.Component {
			return []componentsV1alpha.Component{fakeComponent}
		},
		GetComponentsCapabilitesFn: func() map[string][]string {
			capsMap := make(map[string][]string)
			capsMap["testComponent"] = []string{"mock.feat.testComponent"}
			return capsMap
		},
		GetSubscriptionsFn: func() []runtimePubsub.Subscription {
			return []runtimePubsub.Subscription{
				{
					PubsubName:      "test",
					Topic:           "topic",
					DeadLetterTopic: "dead",
					Metadata:        map[string]string{},
					Rules: []*runtimePubsub.Rule{
						{
							Match: &expr.Expr{},
							Path:  "path",
						},
					},
				},
			}
		},
		ExtendedMetadata: map[string]string{
			"testKey": "testValue",
		},
	}

	response, err := fakeAPI.GetMetadata(context.Background(), &emptypb.Empty{})
	require.NoError(t, err, "Expected no error")
	assert.Equal(t, response.Id, "fakeAPI")
	assert.Len(t, response.RegisteredComponents, 1, "One component should be returned")
	assert.Equal(t, response.RegisteredComponents[0].Name, "testComponent")
	assert.Contains(t, response.ExtendedMetadata, "testKey")
	assert.Equal(t, response.ExtendedMetadata["testKey"], "testValue")
	assert.Equal(t, response.ExtendedMetadata[daprRuntimeVersionKey], buildinfo.Version())
	assert.Len(t, response.RegisteredComponents[0].Capabilities, 1, "One capabilities should be returned")
	assert.Equal(t, response.RegisteredComponents[0].Capabilities[0], "mock.feat.testComponent")
	assert.Equal(t, response.GetActiveActorsCount()[0].Type, "abcd")
	assert.Equal(t, response.GetActiveActorsCount()[0].Count, int32(10))
	assert.Len(t, response.Subscriptions, 1)
	assert.Equal(t, response.Subscriptions[0].PubsubName, "test")
	assert.Equal(t, response.Subscriptions[0].Topic, "topic")
	assert.Equal(t, response.Subscriptions[0].DeadLetterTopic, "dead")
	assert.Equal(t, response.Subscriptions[0].PubsubName, "test")
	assert.Len(t, response.Subscriptions[0].Rules.Rules, 1)
	assert.Equal(t, fmt.Sprintf("%s", response.Subscriptions[0].Rules.Rules[0].Match), "")
	assert.Equal(t, response.Subscriptions[0].Rules.Rules[0].Path, "path")
}

func TestSetMetadata(t *testing.T) {
	fakeComponent := componentsV1alpha.Component{}
	fakeComponent.Name = "testComponent"
	fakeAPI := &UniversalAPI{
		AppID: "fakeAPI",
	}

	_, err := fakeAPI.SetMetadata(context.Background(), &runtimev1pb.SetMetadataRequest{
		Key:   "testKey",
		Value: "testValue",
	})
	require.NoError(t, err, "Expected no error")
	assert.Equal(t, map[string]string{"testKey": "testValue"}, fakeAPI.ExtendedMetadata)
}
