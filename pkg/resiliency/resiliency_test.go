// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

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
)

type mockOperator struct {
	operatorv1pb.UnimplementedOperatorServer
}

func (mockOperator) GetResiliency(context.Context, *operatorv1pb.GetResiliencyRequest) (*operatorv1pb.GetResiliencyResponse, error) {
	resiliency := resiliency_v1alpha.Resiliency{
		Spec: resiliency_v1alpha.ResiliencySpec{
			Policies: resiliency_v1alpha.Policy{
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
			BuildingBlocks: resiliency_v1alpha.BuildingBlocks{
				Services: map[string]resiliency_v1alpha.Service{
					"appB": {
						Timeout:        "general",
						Retry:          "general",
						CircuitBreaker: "general",
					},
				},
				Actors: map[string]resiliency_v1alpha.Actor{
					"myActorType": {
						Timeout:                 "general",
						Retry:                   "general",
						CircuitBreaker:          "general",
						CircuitBreakerScope:     "both",
						CircuitBreakerCacheSize: 5000,
					},
				},
				Components: map[string]resiliency_v1alpha.Component{
					"statestore1": {
						Timeout:        "general",
						Retry:          "general",
						CircuitBreaker: "general",
					},
				},
				Routes: map[string]resiliency_v1alpha.Route{
					"dsstatus.v3": {
						Timeout:        "general",
						Retry:          "general",
						CircuitBreaker: "general",
					},
				},
			},
		},
	}

	body, _ := json.Marshal(resiliency)

	return &operatorv1pb.GetResiliencyResponse{
		Resiliency: body,
	}, nil
}

func getOperatorClient(address string) operatorv1pb.OperatorClient {
	conn, _ := grpc.Dial(address, grpc.WithInsecure())
	return operatorv1pb.NewOperatorClient(conn)
}

func TestLoadStandaloneResiliency(t *testing.T) {
	resiliency, err := LoadStandaloneResiliency("./testdata/resiliency.yaml")
	assert.NoError(t, err)
	assert.NotNil(t, resiliency)

	// Assert the basics.
	assert.NotNil(t, resiliency.Resiliency.Spec)
	assert.NotNil(t, resiliency.Resiliency.Spec.Policies)
	policies := resiliency.Resiliency.Spec.Policies
	assert.NotNil(t, policies)
	buildingBlocks := resiliency.Resiliency.Spec.BuildingBlocks
	assert.NotNil(t, buildingBlocks)

	// Policy checks.
	// Check timeouts.
	timeouts := policies.Timeouts
	assert.NotNil(t, timeouts)
	assert.Len(t, timeouts, 3)
	assert.Equal(t, resiliency.GetTimeoutOrDefault("general"), "5s")
	assert.Equal(t, resiliency.GetTimeoutOrDefault("important"), "60s")
	assert.Equal(t, resiliency.GetTimeoutOrDefault("largeResponse"), "10s")
	assert.Equal(t, resiliency.GetTimeoutOrDefault("default"), "15s")

	// Check retries.
	retries := policies.Retries
	assert.NotNil(t, retries)
	assert.Contains(t, retries, "important")
	importantRetry := resiliency.GetRetryPolicyOrDefault("important")
	assert.Equal(t, importantRetry.Policy, "constant")
	assert.Equal(t, importantRetry.Duration, "5s")
	assert.Equal(t, importantRetry.MaxRetries, 30)
	assert.Contains(t, retries, "largeResponse")
	largeResponseRetry := resiliency.GetRetryPolicyOrDefault("largeResponse")
	assert.Equal(t, largeResponseRetry.Policy, "constant")
	assert.Equal(t, largeResponseRetry.Duration, "5s")
	assert.Equal(t, largeResponseRetry.MaxRetries, 3)
	assert.Contains(t, retries, "pubsubRetry")
	pubsubRetry := resiliency.GetRetryPolicyOrDefault("pubsubRetry")
	assert.Equal(t, pubsubRetry.Policy, "constant")
	assert.Equal(t, pubsubRetry.Duration, "5s")
	assert.Equal(t, pubsubRetry.MaxRetries, 10)
	assert.Contains(t, retries, "retryForever")
	retryForever := resiliency.GetRetryPolicyOrDefault("retryForever")
	assert.Equal(t, retryForever.Policy, "exponential")
	assert.Equal(t, retryForever.MaxInterval, "15s")
	assert.Equal(t, retryForever.MaxRetries, 0)
	assert.Contains(t, retries, "someOperation")
	someOperationRetry := resiliency.GetRetryPolicyOrDefault("someOperation")
	assert.Equal(t, someOperationRetry.Policy, "exponential")
	assert.Equal(t, someOperationRetry.MaxInterval, "15s")
	defaultRetry := resiliency.GetRetryPolicyOrDefault("default")
	assert.Equal(t, defaultRetry.Policy, "constant")
	assert.Equal(t, defaultRetry.Duration, "1s")
	assert.Equal(t, defaultRetry.MaxInterval, "10s")
	assert.Equal(t, defaultRetry.MaxRetries, 10)

	// Check circuit breakers.
	circuitBreakers := policies.CircuitBreakers
	assert.NotNil(t, circuitBreakers)
	pubsubCB := resiliency.GetCircuitBreakerOrDefault("pubsubCB")
	assert.Equal(t, 1, pubsubCB.MaxRequests)
	assert.Equal(t, "8s", pubsubCB.Interval)
	assert.Equal(t, "45s", pubsubCB.Timeout)
	assert.Equal(t, "consecutiveFailures > 8", pubsubCB.Trip)
	defaultCB := resiliency.GetCircuitBreakerOrDefault("default")
	assert.Equal(t, 10, defaultCB.MaxRequests)
	assert.Equal(t, "5s", defaultCB.Interval)
	assert.Equal(t, "10s", defaultCB.Timeout)
	assert.Equal(t, "consecutiveFailures > 5", defaultCB.Trip)

	// BuildingBlocks checks.
	// Check services.
	services := buildingBlocks.Services
	assert.NotNil(t, services)
	assert.Contains(t, services, "appB")
	assert.Equal(t, "general", services["appB"].Timeout)
	assert.Equal(t, "general", services["appB"].Retry)
	assert.Equal(t, "general", services["appB"].CircuitBreaker)

	// Check actors.
	actors := buildingBlocks.Actors
	assert.NotNil(t, actors)
	assert.Contains(t, actors, "myActorType")
	assert.Equal(t, "general", actors["myActorType"].Timeout)
	assert.Equal(t, "general", actors["myActorType"].Retry)
	assert.Equal(t, "general", actors["myActorType"].CircuitBreaker)
	assert.Equal(t, "both", actors["myActorType"].CircuitBreakerScope)
	assert.Equal(t, 5000, actors["myActorType"].CircuitBreakerCacheSize)

	// Check components.
	components := buildingBlocks.Components
	assert.NotNil(t, components)
	assert.Contains(t, components, "statestore1")
	assert.Contains(t, components, "pubsub1")
	assert.Contains(t, components, "pubsub2")
	assert.Equal(t, "general", components["statestore1"].Timeout)
	assert.Equal(t, "general", components["statestore1"].Retry)
	assert.Equal(t, "general", components["statestore1"].CircuitBreaker)
	assert.Equal(t, "pubsubRetry", components["pubsub1"].Retry)
	assert.Equal(t, "pubsubCB", components["pubsub1"].CircuitBreaker)
	assert.Equal(t, "pubsubRetry", components["pubsub2"].Retry)
	assert.Equal(t, "pubsubCB", components["pubsub2"].CircuitBreaker)

	// Check routes.
	routes := buildingBlocks.Routes
	assert.NotNil(t, routes)
	assert.Contains(t, routes, "dsstatus.v3")
	assert.Equal(t, "general", routes["dsstatus.v3"].Timeout)
	assert.Equal(t, "general", routes["dsstatus.v3"].Retry)
	assert.Equal(t, "general", routes["dsstatus.v3"].CircuitBreaker)
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

	resiliency, err := LoadKubernetesResiliency("default", "default", getOperatorClient(fmt.Sprintf("localhost:%d", port)))
	assert.NoError(t, err)
	assert.NotNil(t, resiliency)

	// Assert the policy section.
	assert.Equal(t, resiliency.GetTimeoutOrDefault("general"), "5s")
	pubsubRetry := resiliency.GetRetryPolicyOrDefault("pubsubRetry")
	assert.Equal(t, pubsubRetry.Policy, "constant")
	assert.Equal(t, pubsubRetry.Duration, "5s")
	assert.Equal(t, pubsubRetry.MaxRetries, 10)
	pubsubCB := resiliency.GetCircuitBreakerOrDefault("pubsubCB")
	assert.Equal(t, 1, pubsubCB.MaxRequests)
	assert.Equal(t, "8s", pubsubCB.Interval)
	assert.Equal(t, "45s", pubsubCB.Timeout)
	assert.Equal(t, "consecutiveFailures > 8", pubsubCB.Trip)

	// Assert the building blocks.
	buildingBlocks := resiliency.Resiliency.Spec.BuildingBlocks
	assert.NotNil(t, buildingBlocks)
	// BuildingBlocks checks.
	// Check services.
	services := buildingBlocks.Services
	assert.NotNil(t, services)
	assert.Contains(t, services, "appB")
	assert.Equal(t, "general", services["appB"].Timeout)
	assert.Equal(t, "general", services["appB"].Retry)
	assert.Equal(t, "general", services["appB"].CircuitBreaker)

	// Check actors.
	actors := buildingBlocks.Actors
	assert.NotNil(t, actors)
	assert.Contains(t, actors, "myActorType")
	assert.Equal(t, "general", actors["myActorType"].Timeout)
	assert.Equal(t, "general", actors["myActorType"].Retry)
	assert.Equal(t, "general", actors["myActorType"].CircuitBreaker)
	assert.Equal(t, "both", actors["myActorType"].CircuitBreakerScope)
	assert.Equal(t, 5000, actors["myActorType"].CircuitBreakerCacheSize)

	// Check components.
	components := buildingBlocks.Components
	assert.NotNil(t, components)
	assert.Contains(t, components, "statestore1")
	assert.Equal(t, "general", components["statestore1"].Timeout)
	assert.Equal(t, "general", components["statestore1"].Retry)
	assert.Equal(t, "general", components["statestore1"].CircuitBreaker)

	// Check routes.
	routes := buildingBlocks.Routes
	assert.NotNil(t, routes)
	assert.Contains(t, routes, "dsstatus.v3")
	assert.Equal(t, "general", routes["dsstatus.v3"].Timeout)
	assert.Equal(t, "general", routes["dsstatus.v3"].Retry)
	assert.Equal(t, "general", routes["dsstatus.v3"].CircuitBreaker)
}
