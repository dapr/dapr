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

package universal

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorsfake "github.com/dapr/dapr/pkg/actors/fake"
	componentsV1alpha "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/expr"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	wfenginefake "github.com/dapr/dapr/pkg/runtime/wfengine/fake"
)

func TestGetMetadata(t *testing.T) {
	fakeComponent := componentsV1alpha.Component{}
	fakeComponent.Name = "testComponent"

	actors := actorsfake.New().WithRuntimeStatus(func() *runtimev1pb.ActorRuntime {
		return &runtimev1pb.ActorRuntime{
			RuntimeStatus: runtimev1pb.ActorRuntime_RUNNING,
			HostReady:     true,
			ActiveActors: []*runtimev1pb.ActiveActorsCount{
				{Type: "abcd", Count: 10},
				{Type: "xyz", Count: 5},
			},
		}
	})
	compStore := compstore.New()
	require.NoError(t, compStore.AddPendingComponentForCommit(fakeComponent))
	require.NoError(t, compStore.CommitPendingComponent())
	compStore.SetProgramaticSubscriptions(runtimePubsub.Subscription{
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
	})

	testcases := []struct {
		name                     string
		HealthCheck              *config.AppHealthConfig
		expectHealthCheckEnabled bool
	}{
		{
			name: "health check enabled",
			HealthCheck: &config.AppHealthConfig{
				ProbeOnly:     true,
				ProbeInterval: 10 * time.Second,
				ProbeTimeout:  5 * time.Second,
				Threshold:     3,
			},
			expectHealthCheckEnabled: true,
		},
		{
			name:                     "health check disabled",
			HealthCheck:              nil,
			expectHealthCheckEnabled: false,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			appConnectionConfig := config.AppConnectionConfig{
				Port:                1234,
				Protocol:            "http",
				ChannelAddress:      "1.2.3.4",
				MaxConcurrency:      10,
				HealthCheckHTTPPath: "/healthz",
				HealthCheck:         tc.HealthCheck,
			}

			fakeAPI := &Universal{
				appID:     "fakeAPI",
				actors:    actors,
				compStore: compStore,
				getComponentsCapabilitiesFn: func() map[string][]string {
					capsMap := make(map[string][]string)
					capsMap["testComponent"] = []string{"mock.feat.testComponent"}
					return capsMap
				},
				extendedMetadata: map[string]string{
					"testKey": "testValue",
				},
				appConnectionConfig: appConnectionConfig,
				globalConfig:        &config.Configuration{},
				workflowEngine: wfenginefake.New().WithRuntimeMetadata(func() *runtimev1pb.MetadataWorkflows {
					return &runtimev1pb.MetadataWorkflows{
						ConnectedWorkers: 1,
					}
				}),
			}

			response, err := fakeAPI.GetMetadata(t.Context(), &runtimev1pb.GetMetadataRequest{})
			require.NoError(t, err, "Expected no error")

			bytes, err := json.Marshal(response)
			require.NoError(t, err)

			healthCheckJSON := "},"
			if tc.expectHealthCheckEnabled {
				healthCheckJSON = `,"health":{"health_check_path":"/healthz","health_probe_interval":"10s","health_probe_timeout":"5s","health_threshold":3}},`
			}

			expectedResponse := `{"id":"fakeAPI",` +
				`"active_actors_count":[{"type":"abcd","count":10},{"type":"xyz","count":5}],` +
				`"registered_components":[{"name":"testComponent","capabilities":["mock.feat.testComponent"]}],` +
				`"extended_metadata":{"daprRuntimeVersion":"edge","testKey":"testValue"},` +
				`"subscriptions":[{"pubsub_name":"test","topic":"topic","rules":{"rules":[{"path":"path"}]},"dead_letter_topic":"dead","type":2}],` +
				`"app_connection_properties":{"port":1234,"protocol":"http","channel_address":"1.2.3.4","max_concurrency":10` +
				healthCheckJSON +
				`"runtime_version":"edge","actor_runtime":{"runtime_status":2,"active_actors":[{"type":"abcd","count":10},{"type":"xyz","count":5}],"host_ready":true},"workflows":{"connected_workers":1}}`
			assert.Equal(t, expectedResponse, string(bytes))
		})
	}
}

func TestSetMetadata(t *testing.T) {
	fakeComponent := componentsV1alpha.Component{}
	fakeComponent.Name = "testComponent"
	fakeAPI := &Universal{
		appID: "fakeAPI",
	}

	_, err := fakeAPI.SetMetadata(t.Context(), &runtimev1pb.SetMetadataRequest{
		Key:   "testKey",
		Value: "testValue",
	})
	require.NoError(t, err, "Expected no error")
	assert.Equal(t, map[string]string{"testKey": "testValue"}, fakeAPI.extendedMetadata)
}
