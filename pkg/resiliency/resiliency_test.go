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
