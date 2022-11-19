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
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/kit/logger"
)

type mockOperator struct {
	operatorv1pb.UnimplementedOperatorServer
}

var log = logger.NewLogger("dapr.test")

func (mockOperator) ListResiliency(context.Context, *operatorv1pb.ListResiliencyRequest) (*operatorv1pb.ListResiliencyResponse, error) {
	resiliency := resiliencyV1alpha.Resiliency{
		TypeMeta: v1.TypeMeta{
			Kind: "Resiliency",
		},
		ObjectMeta: v1.ObjectMeta{
			Name: "resiliency",
		},
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Timeouts: map[string]string{
					"general": "5s",
				},
				Retries: map[string]resiliencyV1alpha.Retry{
					"pubsubRetry": {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: 10,
					},
				},
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					"pubsubCB": {
						Interval:    "8s",
						Timeout:     "45s",
						Trip:        "consecutiveFailures > 8",
						MaxRequests: 1,
					},
				},
			},
			Targets: resiliencyV1alpha.Targets{
				Apps: map[string]resiliencyV1alpha.EndpointPolicyNames{
					"appB": {
						Timeout:                 "general",
						Retry:                   "general",
						CircuitBreaker:          "general",
						CircuitBreakerCacheSize: 100,
					},
				},
				Actors: map[string]resiliencyV1alpha.ActorPolicyNames{
					"myActorType": {
						Timeout:                 "general",
						Retry:                   "general",
						CircuitBreaker:          "general",
						CircuitBreakerScope:     "both",
						CircuitBreakerCacheSize: 5000,
					},
				},
				Components: map[string]resiliencyV1alpha.ComponentPolicyNames{
					"statestore1": {
						Outbound: resiliencyV1alpha.PolicyNames{
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

	resiliencyWithScope := resiliencyV1alpha.Resiliency{
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Timeouts: map[string]string{
					"general": "5s",
				},
				Retries: map[string]resiliencyV1alpha.Retry{
					"pubsubRetry": {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: 10,
					},
				},
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					"pubsubCB": {
						Interval:    "8s",
						Timeout:     "45s",
						Trip:        "consecutiveFailures > 8",
						MaxRequests: 1,
					},
				},
			},
			Targets: resiliencyV1alpha.Targets{
				Apps: map[string]resiliencyV1alpha.EndpointPolicyNames{
					"appB": {
						Timeout:                 "general",
						Retry:                   "general",
						CircuitBreaker:          "general",
						CircuitBreakerCacheSize: 100,
					},
				},
				Actors: map[string]resiliencyV1alpha.ActorPolicyNames{
					"myActorType": {
						Timeout:                 "general",
						Retry:                   "general",
						CircuitBreaker:          "general",
						CircuitBreakerScope:     "both",
						CircuitBreakerCacheSize: 5000,
					},
				},
				Components: map[string]resiliencyV1alpha.ComponentPolicyNames{
					"statestore1": {
						Outbound: resiliencyV1alpha.PolicyNames{
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
	conn, _ := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
				return r.ComponentOutboundPolicy(ctx, "statestore1", "Statestore")
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
	r := &resiliencyV1alpha.Resiliency{
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Timeouts: map[string]string{
					"myTimeout": "2s",
				},
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					"myCB1": {
						Interval:    "1s",
						Timeout:     "5s",
						Trip:        "consecutiveFailures > 1",
						MaxRequests: 1,
					},
					"myCB2": {
						Interval:    "2s",
						Timeout:     "10s",
						Trip:        "consecutiveFailures > 2",
						MaxRequests: 2,
					},
				},
				Retries: map[string]resiliencyV1alpha.Retry{
					"myRetry": {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: 3,
					},
				},
			},
			Targets: resiliencyV1alpha.Targets{
				Apps: map[string]resiliencyV1alpha.EndpointPolicyNames{
					"definedApp": {
						Timeout:        "myTimeout",
						CircuitBreaker: "myCB1",
					},
				},
				Actors: map[string]resiliencyV1alpha.ActorPolicyNames{
					"definedActor": {
						Timeout: "myTimeout",
					},
				},
				Components: map[string]resiliencyV1alpha.ComponentPolicyNames{
					"definedComponent": {
						Inbound: resiliencyV1alpha.PolicyNames{
							Timeout:        "myTimeout",
							CircuitBreaker: "myCB1",
						},
						Outbound: resiliencyV1alpha.PolicyNames{
							Timeout:        "myTimeout",
							CircuitBreaker: "myCB2",
							Retry:          "myRetry",
						},
					},
				},
			},
		},
	}
	config := FromConfigurations(log, r)

	assert.Nil(t, config.GetPolicy("badApp", &EndpointPolicy{}))
	assert.Nil(t, config.GetPolicy("badActor", &ActorPolicy{}))
	assert.Nil(t, config.GetPolicy("badComponent", &ComponentInboundPolicy))
	assert.Nil(t, config.GetPolicy("badComponent", &ComponentOutboundPolicy))

	endpointPolicy := config.GetPolicy("definedApp", &EndpointPolicy{})
	assert.NotNil(t, endpointPolicy)
	assert.Equal(t, endpointPolicy.TimeoutPolicy, 2*time.Second)
	assert.Equal(t, endpointPolicy.CircuitBreaker.MaxRequests, uint32(1))
	assert.Nil(t, endpointPolicy.RetryPolicy)

	actorPolicy := config.GetPolicy("definedActor", &ActorPolicy{})
	assert.NotNil(t, actorPolicy)
	assert.Equal(t, actorPolicy.TimeoutPolicy, 2*time.Second)
	assert.Nil(t, actorPolicy.CircuitBreaker)
	assert.Nil(t, actorPolicy.RetryPolicy)

	componentOutboundPolicy := config.GetPolicy("definedComponent", &ComponentOutboundPolicy)
	assert.NotNil(t, componentOutboundPolicy)
	assert.Equal(t, componentOutboundPolicy.TimeoutPolicy, 2*time.Second)
	assert.Equal(t, componentOutboundPolicy.CircuitBreaker.MaxRequests, uint32(2))
	assert.Equal(t, componentOutboundPolicy.RetryPolicy.MaxRetries, int64(3))

	componentInboundPolicy := config.GetPolicy("definedComponent", &ComponentInboundPolicy)
	assert.NotNil(t, componentInboundPolicy)
	assert.Equal(t, componentInboundPolicy.TimeoutPolicy, 2*time.Second)
	assert.Equal(t, componentInboundPolicy.CircuitBreaker.MaxRequests, uint32(1))
	assert.Nil(t, componentInboundPolicy.RetryPolicy)
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
	config := &resiliencyV1alpha.Resiliency{
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Retries: map[string]resiliencyV1alpha.Retry{
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
	config := &resiliencyV1alpha.Resiliency{
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Retries: map[string]resiliencyV1alpha.Retry{
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

func TestDefaultPolicyInterpolation(t *testing.T) {
	r := FromConfigurations(log)

	// Retry
	typePolicies, topPolicy := r.expandPolicyTemplate(&EndpointPolicy{}, DefaultRetryTemplate)
	assert.Len(t, typePolicies, 1)
	assert.Equal(t, "DefaultAppRetryPolicy", typePolicies[0])
	assert.Equal(t, "DefaultRetryPolicy", topPolicy)

	// Timeout
	typePolicies, topPolicy = r.expandPolicyTemplate(&ActorPolicy{}, DefaultTimeoutTemplate)
	assert.Len(t, typePolicies, 1)
	assert.Equal(t, "DefaultActorTimeoutPolicy", typePolicies[0])
	assert.Equal(t, "DefaultTimeoutPolicy", topPolicy)

	// Circuit Breaker (also testing component policy type)
	typePolicies, topPolicy = r.expandPolicyTemplate(&ComponentPolicy{componentType: "Statestore", componentDirection: "Outbound"},
		DefaultCircuitBreakerTemplate)
	assert.Len(t, typePolicies, 3)
	assert.Equal(t, "DefaultStatestoreComponentOutboundCircuitBreakerPolicy", typePolicies[0])
	assert.Equal(t, "DefaultComponentOutboundCircuitBreakerPolicy", typePolicies[1])
	assert.Equal(t, "DefaultComponentCircuitBreakerPolicy", typePolicies[2])
	assert.Equal(t, "DefaultCircuitBreakerPolicy", topPolicy)
}

func TestGetDefaultPolicy(t *testing.T) {
	config := &resiliencyV1alpha.Resiliency{
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Retries: map[string]resiliencyV1alpha.Retry{
					fmt.Sprintf(string(DefaultRetryTemplate), "App"): {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: 10,
					},

					fmt.Sprintf(string(DefaultRetryTemplate), ""): {
						Policy:     "constant",
						Duration:   "1s",
						MaxRetries: 5,
					},
				},
				Timeouts: map[string]string{
					fmt.Sprintf(string(DefaultTimeoutTemplate), "Actor"): "300s",
					fmt.Sprintf(string(DefaultTimeoutTemplate), ""):      "60s",
				},
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					fmt.Sprintf(string(DefaultCircuitBreakerTemplate), "Component"): {
						MaxRequests: 1,
						Interval:    "30s",
						Trip:        "consecutiveFailures > 1",
						Timeout:     "60s",
					},
					fmt.Sprintf(string(DefaultCircuitBreakerTemplate), ""): {
						MaxRequests: 1,
						Interval:    "30s",
						Trip:        "consecutiveFailures > 1",
						Timeout:     "60s",
					},
				},
			},
		},
	}

	r := FromConfigurations(log, config)

	retryName := r.getDefaultRetryPolicy(&EndpointPolicy{})
	assert.Equal(t, "DefaultAppRetryPolicy", retryName)
	retryName = r.getDefaultRetryPolicy(&ActorPolicy{})
	assert.Equal(t, "DefaultRetryPolicy", retryName)

	timeoutName := r.getDefaultTimeoutPolicy(&ActorPolicy{})
	assert.Equal(t, "DefaultActorTimeoutPolicy", timeoutName)
	timeoutName = r.getDefaultTimeoutPolicy(&ComponentPolicy{})
	assert.Equal(t, "DefaultTimeoutPolicy", timeoutName)

	cbName := r.getDefaultCircuitBreakerPolicy(&ComponentPolicy{})
	assert.Equal(t, "DefaultComponentCircuitBreakerPolicy", cbName)
	cbName = r.getDefaultCircuitBreakerPolicy(&EndpointPolicy{})
	assert.Equal(t, "DefaultCircuitBreakerPolicy", cbName)

	// Delete the top-level defaults and make sure we return an empty string if nothing is defined.
	delete(r.retries, "DefaultRetryPolicy")
	delete(r.timeouts, "DefaultTimeoutPolicy")
	delete(r.circuitBreakers, "DefaultCircuitBreakerPolicy")

	retryName = r.getDefaultRetryPolicy(&ActorPolicy{})
	assert.Equal(t, "", retryName)
	timeoutName = r.getDefaultTimeoutPolicy(&ComponentPolicy{})
	assert.Equal(t, "", timeoutName)
	cbName = r.getDefaultCircuitBreakerPolicy(&EndpointPolicy{})
	assert.Equal(t, "", cbName)
}

func TestDefaultPoliciesAreUsedIfNoTargetPolicyExists(t *testing.T) {
	config := &resiliencyV1alpha.Resiliency{
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Retries: map[string]resiliencyV1alpha.Retry{
					"testRetry": {
						Policy:     "constant",
						Duration:   "10ms",
						MaxRetries: 5,
					},
					fmt.Sprintf(string(DefaultRetryTemplate), "App"): {
						Policy:     "constant",
						Duration:   "10ms",
						MaxRetries: 10,
					},

					fmt.Sprintf(string(DefaultRetryTemplate), ""): {
						Policy:     "constant",
						Duration:   "10ms",
						MaxRetries: 3,
					},
				},
				Timeouts: map[string]string{
					fmt.Sprintf(string(DefaultTimeoutTemplate), ""): "100ms",
				},
				CircuitBreakers: map[string]resiliencyV1alpha.CircuitBreaker{
					fmt.Sprintf(string(DefaultCircuitBreakerTemplate), ""): {
						Trip:        "consecutiveFailures > 1",
						MaxRequests: 1,
						Timeout:     "60s",
					},
				},
			},
			Targets: resiliencyV1alpha.Targets{
				Apps: map[string]resiliencyV1alpha.EndpointPolicyNames{
					"testApp": {
						Retry: "testRetry",
					},
				},
			},
		},
	}

	r := FromConfigurations(log, config)

	// Targeted App
	policy := r.EndpointPolicy(context.Background(), "testApp", "localhost")
	count := atomic.Int64{}
	policy(func(ctx context.Context) error {
		count.Add(1)
		return errors.New("Forced failure")
	})
	assert.Equal(t, int64(6), count.Load())

	// Generic App
	policy = r.EndpointPolicy(context.Background(), "noMatchingTarget", "localhost")
	count.Store(0)
	policy(func(ctx context.Context) error {
		count.Add(1)
		return errors.New("Forced failure")
	})
	assert.Equal(t, int64(11), count.Load())

	// Not defined
	policy = r.ActorPreLockPolicy(context.Background(), "actorType", "actorID")
	count.Store(0)
	policy(func(ctx context.Context) error {
		count.Add(1)
		return errors.New("Forced failure")
	})
	assert.Equal(t, int64(4), count.Load())

	// One last one for ActorPostLock which just includes timeouts.
	policy = r.ActorPostLockPolicy(context.Background(), "actorType", "actorID")
	count.Store(0)
	start := time.Now()
	err := policy(func(ctx context.Context) error {
		count.Add(1)
		time.Sleep(time.Second * 5)
		return errors.New("Forced failure")
	})
	assert.Less(t, time.Since(start), time.Second*5)
	assert.Equal(t, int64(1), count.Load())           // Post lock policies don't have a retry, only pre lock do.
	assert.NotEqual(t, "Forced failure", err.Error()) // We should've timed out instead.
}
