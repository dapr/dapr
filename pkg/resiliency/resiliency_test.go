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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
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
						MaxRetries: ptr.Of(10),
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
						MaxRetries: ptr.Of(10),
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
	configs := LoadLocalResiliency(log, "default", "./testdata")
	assert.Len(t, configs, 1)
	r := FromConfigurations(log, configs...)

	tests := []struct {
		name   string
		create func(r *Resiliency) Runner[any]
	}{
		{
			name: "component",
			create: func(r *Resiliency) Runner[any] {
				return NewRunner[any](ctx, r.ComponentOutboundPolicy("statestore1", "Statestore"))
			},
		},
		{
			name: "endpoint",
			create: func(r *Resiliency) Runner[any] {
				return NewRunner[any](ctx, r.EndpointPolicy("appB", "127.0.0.1:3500"))
			},
		},
		{
			name: "actor",
			create: func(r *Resiliency) Runner[any] {
				return NewRunner[any](ctx, r.ActorPreLockPolicy("myActorType", "id"))
			},
		},
		{
			name: "actor post lock",
			create: func(r *Resiliency) Runner[any] {
				return NewRunner[any](ctx, r.ActorPostLockPolicy("myActorType", "id"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.create(r)
			called := atomic.Bool{}
			_, err := p(func(ctx context.Context) (any, error) {
				called.Store(true)
				return nil, nil
			})
			require.NoError(t, err)
			assert.True(t, called.Load())
		})
	}
}

func TestLoadKubernetesResiliency(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err)

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
		configs := LoadLocalResiliency(log, "app1", "./testdata")
		assert.NotNil(t, configs)
		assert.Len(t, configs, 2)
		assert.Equal(t, "Resiliency", configs[0].Kind)
		assert.Equal(t, "resiliency", configs[0].Name)
		assert.Equal(t, "Resiliency", configs[1].Kind)
		assert.Equal(t, "resiliency", configs[1].Name)
	})

	t.Run("test load resiliency skips other types", func(t *testing.T) {
		configs := LoadLocalResiliency(log, "app1", "../components")
		assert.NotNil(t, configs)
		assert.Empty(t, configs)
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
				require.NoError(t, err)
				assert.Equal(t, tt.output, actual)
			} else {
				require.EqualError(t, err, tt.err)
			}
		})
	}
}

func TestParseMaxRetries(t *testing.T) {
	configs := LoadLocalResiliency(log, "app1", "./testdata")
	require.NotNil(t, configs)
	require.Len(t, configs, 2)
	require.NotNil(t, configs[0])

	r := FromConfigurations(log, configs[0])
	require.NotEmpty(t, r.retries)
	require.NotNil(t, r.retries["noRetry"])
	require.NotNil(t, r.retries["retryForever"])
	require.NotNil(t, r.retries["missingMaxRetries"])
	require.NotNil(t, r.retries["important"])

	// important has "maxRetries: 30"
	assert.Equal(t, int64(30), r.retries["important"].MaxRetries)
	// noRetry has "maxRetries: 0" (no retries)
	assert.Equal(t, int64(0), r.retries["noRetry"].MaxRetries)
	// retryForever has "maxRetries: -1" (retry forever)
	assert.Equal(t, int64(-1), r.retries["retryForever"].MaxRetries)
	// missingMaxRetries has no "maxRetries" so should default to -1
	assert.Equal(t, int64(-1), r.retries["missingMaxRetries"].MaxRetries)
}

func TestResiliencyScopeIsRespected(t *testing.T) {
	port, _ := freeport.GetFreePort()
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err)

	s := grpc.NewServer()
	operatorv1pb.RegisterOperatorServer(s, &mockOperator{})
	defer s.Stop()

	go func() {
		s.Serve(lis)
	}()

	time.Sleep(time.Second * 1)

	resiliencies := LoadLocalResiliency(log, "app1", "./testdata")
	assert.Len(t, resiliencies, 2)

	resiliencies = LoadKubernetesResiliency(log, "app2", "default", getOperatorClient(fmt.Sprintf("localhost:%d", port)))
	assert.Len(t, resiliencies, 2)

	resiliencies = LoadLocalResiliency(log, "app2", "./testdata")
	assert.Len(t, resiliencies, 2)

	resiliencies = LoadLocalResiliency(log, "app3", "./testdata")
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
						MaxRetries: ptr.Of(3),
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

	assert.False(t, config.PolicyDefined("badApp", EndpointPolicy{}))
	assert.False(t, config.PolicyDefined("badActor", ActorPolicy{}))
	assert.False(t, config.PolicyDefined("badComponent", ComponentInboundPolicy))
	assert.False(t, config.PolicyDefined("badComponent", ComponentOutboundPolicy))

	assert.True(t, config.PolicyDefined("definedApp", EndpointPolicy{}))
	assert.True(t, config.PolicyDefined("definedActor", ActorPolicy{}))
	assert.True(t, config.PolicyDefined("definedComponent", ComponentPolicy{}))
	assert.True(t, config.PolicyDefined("definedComponent", ComponentOutboundPolicy))
	assert.True(t, config.PolicyDefined("definedComponent", ComponentInboundPolicy))
}

func TestResiliencyHasBuiltInPolicy(t *testing.T) {
	r := FromConfigurations(log)
	assert.NotNil(t, r)

	builtins := []BuiltInPolicyName{
		BuiltInServiceRetries,
		BuiltInActorRetries,
		BuiltInActorReminderRetries,
		BuiltInInitializationRetries,
	}
	for _, n := range builtins {
		p := r.BuiltInPolicy(n)
		_ = assert.NotNil(t, p) &&
			assert.NotNil(t, p.r)
	}
}

func TestResiliencyCannotLowerBuiltInRetriesPastThree(t *testing.T) {
	config := &resiliencyV1alpha.Resiliency{
		Spec: resiliencyV1alpha.ResiliencySpec{
			Policies: resiliencyV1alpha.Policies{
				Retries: map[string]resiliencyV1alpha.Retry{
					string(BuiltInServiceRetries): {
						Policy:     "constant",
						Duration:   "5s",
						MaxRetries: ptr.Of(1),
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
						MaxRetries: ptr.Of(10),
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
						MaxRetries: ptr.Of(10),
					},

					fmt.Sprintf(string(DefaultRetryTemplate), ""): {
						Policy:     "constant",
						Duration:   "1s",
						MaxRetries: ptr.Of(5),
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
						MaxRetries: ptr.Of(5),
					},
					fmt.Sprintf(string(DefaultRetryTemplate), "App"): {
						Policy:     "constant",
						Duration:   "10ms",
						MaxRetries: ptr.Of(10),
					},

					fmt.Sprintf(string(DefaultRetryTemplate), ""): {
						Policy:     "constant",
						Duration:   "10ms",
						MaxRetries: ptr.Of(3),
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
					fmt.Sprintf(string(DefaultCircuitBreakerTemplate), "App"): {
						Trip:        "consecutiveFailures > 15",
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
	policy := NewRunner[any](context.Background(),
		r.EndpointPolicy("testApp", "localhost"),
	)
	count := atomic.Int64{}
	policy(func(ctx context.Context) (any, error) {
		count.Add(1)
		return nil, errors.New("Forced failure")
	})
	assert.Equal(t, int64(6), count.Load())

	// Generic App
	concurrentPolicyExec(t, func(idx int) *PolicyDefinition {
		return r.EndpointPolicy(fmt.Sprintf("noMatchingTarget-%d", idx), "localhost")
	}, 11) // App has a CB that trips after 15 failure, so we don't trip it but still do all the 10 default retries

	// execute concurrent to get coverage
	concurrentPolicyExec(t, func(idx int) *PolicyDefinition {
		return r.ActorPreLockPolicy(fmt.Sprintf("actorType-%d", idx), "actorID")
	}, 2) // actorType is not a known target, so we get 1 retry + original call as default circuit breaker trips (consecutiveFailures > 1)

	// One last one for ActorPostLock which just includes timeouts.
	policy = NewRunner[any](context.Background(),
		r.ActorPostLockPolicy("actorType", "actorID"),
	)
	count.Store(0)
	start := time.Now()
	_, err := policy(func(ctx context.Context) (any, error) {
		count.Add(1)
		time.Sleep(time.Second * 5)
		return nil, errors.New("Forced failure")
	})
	assert.Less(t, time.Since(start), time.Second*5)
	assert.Equal(t, int64(1), count.Load())           // Post lock policies don't have a retry, only pre lock do.
	assert.NotEqual(t, "Forced failure", err.Error()) // We should've timed out instead.
}

func concurrentPolicyExec(t *testing.T, policyDefFn func(idx int) *PolicyDefinition, wantCount int64) {
	t.Helper()
	wg := sync.WaitGroup{}
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func(i int) {
			defer wg.Done()
			// Not defined
			policy := NewRunner[any](context.Background(), policyDefFn(i))
			count := atomic.Int64{}
			count.Store(0)
			policy(func(ctx context.Context) (any, error) {
				count.Add(1)
				return nil, errors.New("forced failure")
			})
			assert.Equal(t, wantCount, count.Load())
		}(i)
	}
	wg.Wait()
}
