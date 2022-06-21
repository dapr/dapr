/*
Copyright 2021 The Dapr Authors
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

package resiliency

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	resiliency_v1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/kit/logger"
)

type mockOperator struct {
	operatorv1pb.UnimplementedOperatorServer
}

var log = logger.NewLogger("dapr.test")

func (mockOperator) ListResiliency(context.Context, *operatorv1pb.ListResiliencyRequest) (*operatorv1pb.ListResiliencyResponse, error) {
	resiliency := resiliency_v1alpha.Resiliency{
		TypeMeta: v1.TypeMeta{
			Kind: "Resiliency",
		},
		ObjectMeta: v1.ObjectMeta{
			Name: "resiliency",
		},
		Spec: resiliency_v1alpha.ResiliencySpec{
			Policies: resiliency_v1alpha.Policies{
				Timeouts: map[string]string{
					"general": "5s",
				},
				Retries: map[string]resiliency_v1alpha.Retry{
					"pubsubRetry": {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: 10,
					},
				},
				CircuitBreakers: map[string]resiliency_v1alpha.CircuitBreaker{
					"pubsubCB": {
						Interval:    "8s",
						Timeout:     "45s",
						Trip:        "consecutiveFailures > 8",
						MaxRequests: 1,
					},
				},
			},
			Targets: resiliency_v1alpha.Targets{
				Apps: map[string]resiliency_v1alpha.EndpointPolicyNames{
					"appB": {
						Timeout:                 "general",
						Retry:                   "general",
						CircuitBreaker:          "general",
						CircuitBreakerCacheSize: 100,
					},
				},
				Actors: map[string]resiliency_v1alpha.ActorPolicyNames{
					"myActorType": {
						Timeout:                 "general",
						Retry:                   "general",
						CircuitBreaker:          "general",
						CircuitBreakerScope:     "both",
						CircuitBreakerCacheSize: 5000,
					},
				},
				Components: map[string]resiliency_v1alpha.ComponentPolicyNames{
					"statestore1": {
						Outbound: resiliency_v1alpha.PolicyNames{
							Timeout:        "general",
							Retry:          "general",
							CircuitBreaker: "general",
						},
					},
				},
			},
		},
	}
	resiliencyBytes, _ := json.Marshal(resiliency)

	resiliencyWithScope := resiliency_v1alpha.Resiliency{
		Spec: resiliency_v1alpha.ResiliencySpec{
			Policies: resiliency_v1alpha.Policies{
				Timeouts: map[string]string{
					"general": "5s",
				},
				Retries: map[string]resiliency_v1alpha.Retry{
					"pubsubRetry": {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: 10,
					},
				},
				CircuitBreakers: map[string]resiliency_v1alpha.CircuitBreaker{
					"pubsubCB": {
						Interval:    "8s",
						Timeout:     "45s",
						Trip:        "consecutiveFailures > 8",
						MaxRequests: 1,
					},
				},
			},
			Targets: resiliency_v1alpha.Targets{
				Apps: map[string]resiliency_v1alpha.EndpointPolicyNames{
					"appB": {
						Timeout:                 "general",
						Retry:                   "general",
						CircuitBreaker:          "general",
						CircuitBreakerCacheSize: 100,
					},
				},
				Actors: map[string]resiliency_v1alpha.ActorPolicyNames{
					"myActorType": {
						Timeout:                 "general",
						Retry:                   "general",
						CircuitBreaker:          "general",
						CircuitBreakerScope:     "both",
						CircuitBreakerCacheSize: 5000,
					},
				},
				Components: map[string]resiliency_v1alpha.ComponentPolicyNames{
					"statestore1": {
						Outbound: resiliency_v1alpha.PolicyNames{
							Timeout:        "general",
							Retry:          "general",
							CircuitBreaker: "general",
						},
					},
				},
			},
		},
		Scopes: []string{"app1", "app2"},
	}
	resiliencyWithScopesBytes, _ := json.Marshal(resiliencyWithScope)

	return &operatorv1pb.ListResiliencyResponse{
		Resiliencies: [][]byte{
			resiliencyBytes,
			resiliencyWithScopesBytes,
		},
	}, nil
}

func getOperatorClient(address string) operatorv1pb.OperatorClient {
	conn, _ := grpc.Dial(address, grpc.WithInsecure())
	return operatorv1pb.NewOperatorClient(conn)
}

func TestPoliciesForTargets(t *testing.T) {
	ctx := context.Background()
	configs := LoadStandaloneResiliency(log, "default", "./testdata")
	assert.Len(t, configs, 1)
	r := FromConfigurations(log, configs...)

	tests := []struct {
		name   string
		create func(r *Resiliency) Runner
	}{
		{
			name: "component",
			create: func(r *Resiliency) Runner {
				return r.ComponentOutboundPolicy(ctx, "statestore1")
			},
		},
		{
			name: "endpoint",
			create: func(r *Resiliency) Runner {
				return r.EndpointPolicy(ctx, "appB", "127.0.0.1:3500")
			},
		},
		{
			name: "actor",
			create: func(r *Resiliency) Runner {
				return r.ActorPreLockPolicy(ctx, "myActorType", "id")
			},
		},
		{
			name: "actor post lock",
			create: func(r *Resiliency) Runner {
				return r.ActorPostLockPolicy(ctx, "myActorType", "id")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.create(r)
			called := false
			err := p(func(ctx context.Context) error {
				called = true
				return nil
			})
			assert.NoError(t, err)
			assert.True(t, called)
		})
	}
}

func TestLoadKubernetesResiliency(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	s := grpc.NewServer()
	operatorv1pb.RegisterOperatorServer(s, &mockOperator{})
	defer s.Stop()

	go func() {
		s.Serve(lis)
	}()

	time.Sleep(time.Second * 1)

	resiliency := LoadKubernetesResiliency(log, "default", "default",
		getOperatorClient(fmt.Sprintf("localhost:%d", port)))
	assert.NotNil(t, resiliency)
	assert.Len(t, resiliency, 1)
	assert.Equal(t, "Resiliency", resiliency[0].TypeMeta.Kind)
	assert.Equal(t, "resiliency", resiliency[0].ObjectMeta.Name)
}

func TestLoadStandaloneResiliency(t *testing.T) {
	t.Run("test load resiliency", func(t *testing.T) {
		configs := LoadStandaloneResiliency(log, "app1", "./testdata")
		assert.NotNil(t, configs)
		assert.Len(t, configs, 2)
		assert.Equal(t, "Resiliency", configs[0].Kind)
		assert.Equal(t, "resiliency", configs[0].Name)
		assert.Equal(t, "Resiliency", configs[1].Kind)
		assert.Equal(t, "resiliency", configs[1].Name)
	})

	t.Run("test load resiliency skips other types", func(t *testing.T) {
		configs := LoadStandaloneResiliency(log, "app1", "../components")
		assert.NotNil(t, configs)
		assert.Len(t, configs, 0)
	})
}

func TestParseActorCircuitBreakerScope(t *testing.T) {
	tests := []struct {
		input  string
		output ActorCircuitBreakerScope
		err    string
	}{
		{
			input:  "type",
			output: ActorCircuitBreakerScopeType,
		},
		{
			input:  "id",
			output: ActorCircuitBreakerScopeID,
		},
		{
			input:  "both",
			output: ActorCircuitBreakerScopeBoth,
		},
		{
			input: "unknown",
			err:   "unknown circuit breaker scope \"unknown\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			actual, err := ParseActorCircuitBreakerScope(tt.input)
			if tt.err == "" {
				assert.NoError(t, err)
				assert.Equal(t, tt.output, actual)
			} else {
				assert.EqualError(t, err, tt.err)
			}
		})
	}
}

func TestResiliencyScopeIsRespected(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	assert.NoError(t, err)

	s := grpc.NewServer()
	operatorv1pb.RegisterOperatorServer(s, &mockOperator{})
	defer s.Stop()

	go func() {
		s.Serve(lis)
	}()

	time.Sleep(time.Second * 1)

	resiliencies := LoadStandaloneResiliency(log, "app1", "./testdata")
	assert.Len(t, resiliencies, 2)

	resiliencies = LoadKubernetesResiliency(log, "app2", "default", getOperatorClient(fmt.Sprintf("localhost:%d", port)))
	assert.Len(t, resiliencies, 2)

	resiliencies = LoadStandaloneResiliency(log, "app2", "./testdata")
	assert.Len(t, resiliencies, 2)

	resiliencies = LoadStandaloneResiliency(log, "app3", "./testdata")
	assert.Len(t, resiliencies, 1)
}

func TestBuiltInPoliciesAreCreated(t *testing.T) {
	r := FromConfigurations(log)
	assert.NotNil(t, r.retries[string(BuiltInServiceRetries)])
	retry := r.retries[string(BuiltInServiceRetries)]
	assert.Equal(t, int64(3), retry.MaxRetries)
	assert.Equal(t, time.Second, retry.Duration)
}

func TestResiliencyHasTargetDefined(t *testing.T) {
	r := &resiliency_v1alpha.Resiliency{
		Spec: resiliency_v1alpha.ResiliencySpec{
			Targets: resiliency_v1alpha.Targets{
				Apps: map[string]resiliency_v1alpha.EndpointPolicyNames{
					"definedApp": {},
				},
				Actors: map[string]resiliency_v1alpha.ActorPolicyNames{
					"definedActor": {},
				},
				Components: map[string]resiliency_v1alpha.ComponentPolicyNames{
					"definedComponent": {},
				},
			},
		},
	}
	config := FromConfigurations(log, r)

	assert.False(t, config.PolicyDefined("badApp", Endpoint))
	assert.False(t, config.PolicyDefined("badActor", Actor))
	assert.False(t, config.PolicyDefined("badComponent", Component))
	assert.True(t, config.PolicyDefined("definedApp", Endpoint))
	assert.True(t, config.PolicyDefined("definedActor", Actor))
	assert.True(t, config.PolicyDefined("definedComponent", Component))
}

func TestResiliencyHasBuiltInPolicy(t *testing.T) {
	r := FromConfigurations(log)
	assert.NotNil(t, r)
	assert.NotNil(t, r.BuiltInPolicy(context.Background(), BuiltInServiceRetries))
	assert.NotNil(t, r.BuiltInPolicy(context.Background(), BuiltInActorRetries))
	assert.NotNil(t, r.BuiltInPolicy(context.Background(), BuiltInActorReminderRetries))
	assert.NotNil(t, r.BuiltInPolicy(context.Background(), BuiltInInitializationRetries))
}

func TestResiliencyCannotLowerBuiltInRetriesPastThree(t *testing.T) {
	config := &resiliency_v1alpha.Resiliency{
		Spec: resiliency_v1alpha.ResiliencySpec{
			Policies: resiliency_v1alpha.Policies{
				Retries: map[string]resiliency_v1alpha.Retry{
					string(BuiltInServiceRetries): {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: 1,
					},
				},
			},
		},
	}
	r := FromConfigurations(log, config)
	assert.NotNil(t, r)
	assert.Equal(t, int64(3), r.retries[string(BuiltInServiceRetries)].MaxRetries)
}

func TestResiliencyProtectedPolicyCannotBeChanged(t *testing.T) {
	config := &resiliency_v1alpha.Resiliency{
		Spec: resiliency_v1alpha.ResiliencySpec{
			Policies: resiliency_v1alpha.Policies{
				Retries: map[string]resiliency_v1alpha.Retry{
					string(BuiltInActorNotFoundRetries): {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: 10,
					},
				},
			},
		},
	}
	r := FromConfigurations(log, config)
	assert.NotNil(t, r)
	assert.Equal(t, int64(5), r.retries[string(BuiltInActorNotFoundRetries)].MaxRetries)
}

func TestResiliencyIsBuiltInPolicy(t *testing.T) {
	r := FromConfigurations(log)
	assert.NotNil(t, r)
	assert.True(t, r.isBuiltInPolicy(string(BuiltInServiceRetries)))
	assert.True(t, r.isBuiltInPolicy(string(BuiltInActorRetries)))
	assert.True(t, r.isBuiltInPolicy(string(BuiltInActorReminderRetries)))
	assert.True(t, r.isBuiltInPolicy(string(BuiltInActorNotFoundRetries)))
	assert.True(t, r.isBuiltInPolicy(string(BuiltInInitializationRetries)))
	assert.False(t, r.isBuiltInPolicy("Not a built in"))
}

func TestResiliencyIsProtectedPolicy(t *testing.T) {
	r := FromConfigurations(log)
	assert.True(t, r.isProtectedPolicy(string(BuiltInActorNotFoundRetries)))
	assert.False(t, r.isProtectedPolicy(string(BuiltInActorRetries)))
	assert.False(t, r.isProtectedPolicy("Random name"))
}
