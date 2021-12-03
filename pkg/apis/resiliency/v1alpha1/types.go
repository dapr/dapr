// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:noStatus
// +kubebuilder:object:root=true

type Resiliency struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec ResiliencySpec `json:"spec,omitempty"`
	// +optional
	Scopes []string `json:"scopes,omitempty"`
}

type ResiliencySpec struct {
	Policies       Policy         `json:"policies"`
	BuildingBlocks BuildingBlocks `json:"buildingBlocks" yaml:"buildingBlocks"`
}

type Policy struct {
	Timeouts        map[string]string         `json:"timeouts,omitempty" yaml:"timeouts,omitempty"`
	Retries         map[string]Retry          `json:"retries,omitempty" yaml:"retries,omitempty"`
	CircuitBreakers map[string]CircuitBreaker `json:"circuitBreakers,omitempty" yaml:"circuitBreakers,omitempty"`
}

type Retry struct {
	Policy      string `json:"policy,omitempty" yaml:"policy,omitempty"`
	Duration    string `json:"duration,omitempty" yaml:"duration,omitempty"`
	MaxInterval string `json:"maxInterval,omitempty" yaml:"maxInterval,omitempty"`
	MaxRetries  int    `json:"maxRetries,omitempty" yaml:"maxRetries,omitempty"`
}

type CircuitBreaker struct {
	MaxRequests int    `json:"maxRequests,omitempty" yaml:"maxRequests,omitempty"`
	Interval    string `json:"interval,omitempty" yaml:"interval,omitempty"`
	Timeout     string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Trip        string `json:"trip,omitempty" yaml:"trip,omitempty"`
}

type BuildingBlocks struct {
	Services   map[string]Service   `json:"services,omitempty" yaml:"services,omitempty"`
	Actors     map[string]Actor     `json:"actors,omitempty" yaml:"actors,omitempty"`
	Components map[string]Component `json:"components,omitempty" yaml:"components,omitempty"`
	Routes     map[string]Route     `json:"routes,omitempty" yaml:"routes,omitempty"`
}

type Service struct {
	Timeout        string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Retry          string `json:"retry,omitempty" yaml:"retry,omitempty"`
	CircuitBreaker string `json:"circuitBreaker,omitempty" yaml:"circuitBreaker,omitempty"`
}

type Actor struct {
	Timeout                 string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Retry                   string `json:"retry,omitempty" yaml:"retry,omitempty"`
	CircuitBreaker          string `json:"circuitBreaker,omitempty" yaml:"circuitBreaker,omitempty"`
	CircuitBreakerScope     string `json:"circuitBreakerScope,omitempty" yaml:"circuitBreakerScope,omitempty"`
	CircuitBreakerCacheSize int    `json:"circuitBreakerCacheSize,omitempty" yaml:"circuitBreakerCacheSize,omitempty"`
}

type Component struct {
	Timeout        string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Retry          string `json:"retry,omitempty" yaml:"retry,omitempty"`
	CircuitBreaker string `json:"circuitBreaker,omitempty" yaml:"circuitBreaker,omitempty"`
}

type Route struct {
	Timeout        string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Retry          string `json:"retry,omitempty" yaml:"retry,omitempty"`
	CircuitBreaker string `json:"circuitBreaker,omitempty" yaml:"circuitBreaker,omitempty"`
}

// +kubebuilder:object:root=true
type ResiliencyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Resiliency `json:"items"`
}
