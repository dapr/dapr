// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package resiliency

import (
	"context"
	"os"
	"path/filepath"
	"time"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	"gopkg.in/yaml.v2"

	resiliency_v1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"

	"github.com/dapr/kit/logger"
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

func LoadStandaloneResiliency(path, runtimeId string, log logger.Logger) []Resiliency {
	var resiliencies []Resiliency

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return resiliencies
	}

	files, err := os.ReadDir(path)
	if err != nil {
		log.Errorf("failed to read resiliences from path %s: %s", err)
		return resiliencies
	}

	for _, file := range files {
		filePath := filepath.Join(path, file.Name())
		b, err := os.ReadFile(filePath)
		if err != nil {
			log.Errorf("Could not read file %s - %s", file.Name(), err.Error())
			continue
		}

		// Parse environment variables from yaml
		b = []byte(os.ExpandEnv(string(b)))
		resiliencies = appendResiliency(resiliencies, b, log)
	}

	return filterResiliencies(resiliencies, runtimeId)
}

func LoadKubernetesResiliency(runtimeId, namespace string, operatorClient operatorv1pb.OperatorClient, log logger.Logger) []Resiliency {
	var resiliences []Resiliency

	resp, err := operatorClient.ListResiliency(context.Background(), &operatorv1pb.ListResiliencyRequest{
		Namespace: namespace,
	}, grpc_retry.WithMax(operatorRetryCount), grpc_retry.WithPerRetryTimeout(operatorTimePerRetry))
	if err != nil {
		log.Errorf("Error listing resiliences: %s", err.Error())
		return resiliences
	}

	if resp.GetResiliencies() == nil {
		log.Debug("No resiliencies found.")
		return resiliences
	}

	for _, body := range resp.GetResiliencies() {
		resiliences = appendResiliency(resiliences, body, log)
	}

	return filterResiliencies(resiliences, runtimeId)
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

func filterResiliencies(resiliences []Resiliency, runtimeId string) []Resiliency {
	var filteredResiliencies []Resiliency

	for _, resiliency := range resiliences {
		if len(resiliency.Resiliency.Scopes) == 0 {
			filteredResiliencies = append(filteredResiliencies, resiliency)
			continue
		}

		for _, scope := range resiliency.Resiliency.Scopes {
			if scope == runtimeId {
				filteredResiliencies = append(filteredResiliencies, resiliency)
				break
			}
		}
	}

	return filteredResiliencies
}

func appendResiliency(list []Resiliency, b []byte, log logger.Logger) []Resiliency {
	resiliency := resiliency_v1alpha.Resiliency{}
	err := yaml.Unmarshal(b, &resiliency)
	if err != nil {
		log.Errorf("Could not parse resiliency: %s", err.Error())
		return list
	}
	return append(list, Resiliency{resiliency})
}
