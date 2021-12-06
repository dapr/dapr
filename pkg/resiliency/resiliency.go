// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package resiliency

import (
	"context"
	"encoding/json"
	"os"
	"time"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"

	resiliency_v1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

const (
	operatorRetryCount   = 100
	operatorTimePerRetry = time.Second * 5
)

// Wrapper to let us define some helper functions without duplicating the domain definition.
type Resiliency struct {
	Resiliency resiliency_v1alpha.Resiliency
}

func LoadDefaultResiliency() *Resiliency {
	return &Resiliency{resiliency_v1alpha.Resiliency{}}
}

func LoadStandaloneResiliency(path string) (*Resiliency, error) {
	_, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// Parse environment variables from yaml
	b = []byte(os.ExpandEnv(string(b)))

	resiliency := &resiliency_v1alpha.Resiliency{}
	err = yaml.Unmarshal(b, resiliency)
	if err != nil {
		return nil, err
	}

	return &Resiliency{*resiliency}, nil
}

func LoadKubernetesResiliency(resiliencyConfig, namespace string, operatorClient operatorv1pb.OperatorClient) (*Resiliency, error) {
	resp, err := operatorClient.GetResiliency(context.Background(), &operatorv1pb.GetResiliencyRequest{
		Name:      resiliencyConfig,
		Namespace: namespace,
	}, grpc_retry.WithMax(operatorRetryCount), grpc_retry.WithPerRetryTimeout(operatorTimePerRetry))
	if err != nil {
		return nil, err
	}

	if resp.GetResiliency() == nil {
		return nil, errors.Errorf("Resilienct %s was not found.", resiliencyConfig)
	}

	resiliency := LoadDefaultResiliency()
	err = json.Unmarshal(resp.GetResiliency(), &resiliency.Resiliency)

	if err != nil {
		return nil, err
	}

	return resiliency, nil
}

func (r *Resiliency) GetRetryPolicyOrDefault(policyName string) *resiliency_v1alpha.Retry {
	if retries, ok := r.Resiliency.Spec.Policies.Retries[policyName]; ok {
		return &retries
	}
	return &resiliency_v1alpha.Retry{
		Policy:      "constant",
		Duration:    "1s",
		MaxInterval: "10s",
		MaxRetries:  10,
	}
}

func (r *Resiliency) GetTimeoutOrDefault(timeoutName string) string {
	if timeout, ok := r.Resiliency.Spec.Policies.Timeouts[timeoutName]; ok {
		return timeout
	}
	return "15s"
}

func (r *Resiliency) GetCircuitBreakerOrDefault(circuitBreakerName string) *resiliency_v1alpha.CircuitBreaker {
	if circuitBreaker, ok := r.Resiliency.Spec.Policies.CircuitBreakers[circuitBreakerName]; ok {
		return &circuitBreaker
	}
	return &resiliency_v1alpha.CircuitBreaker{
		MaxRequests: 10,
		Interval:    "5s",
		Timeout:     "10s",
		Trip:        "consecutiveFailures > 5",
	}
}
