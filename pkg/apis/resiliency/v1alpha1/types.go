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

package v1alpha1

import (
	"encoding/json"

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

// String implements fmt.Stringer and is used for debugging. It returns the policy object encoded as JSON.
func (r Resiliency) String() string {
	b, _ := json.Marshal(r)
	return string(b)
}

type ResiliencySpec struct {
	Policies Policies `json:"policies"`
	Targets  Targets  `json:"targets" yaml:"targets"`
}

type Policies struct {
	Timeouts        map[string]string         `json:"timeouts,omitempty" yaml:"timeouts,omitempty"`
	Retries         map[string]Retry          `json:"retries,omitempty" yaml:"retries,omitempty"`
	CircuitBreakers map[string]CircuitBreaker `json:"circuitBreakers,omitempty" yaml:"circuitBreakers,omitempty"`
}

type Retry struct {
	Policy      string `json:"policy,omitempty" yaml:"policy,omitempty"`
	Duration    string `json:"duration,omitempty" yaml:"duration,omitempty"`
	MaxInterval string `json:"maxInterval,omitempty" yaml:"maxInterval,omitempty"`
	MaxRetries  *int   `json:"maxRetries,omitempty" yaml:"maxRetries,omitempty"`
}

type CircuitBreaker struct {
	MaxRequests int    `json:"maxRequests,omitempty" yaml:"maxRequests,omitempty"`
	Interval    string `json:"interval,omitempty" yaml:"interval,omitempty"`
	Timeout     string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Trip        string `json:"trip,omitempty" yaml:"trip,omitempty"`
}

type Targets struct {
	Apps       map[string]EndpointPolicyNames  `json:"apps,omitempty" yaml:"apps,omitempty"`
	Actors     map[string]ActorPolicyNames     `json:"actors,omitempty" yaml:"actors,omitempty"`
	Components map[string]ComponentPolicyNames `json:"components,omitempty" yaml:"components,omitempty"`
}

type ComponentPolicyNames struct {
	Inbound  PolicyNames `json:"inbound,omitempty" yaml:"inbound,omitempty"`
	Outbound PolicyNames `json:"outbound,omitempty" yaml:"outbound,omitempty"`
}

type PolicyNames struct {
	Timeout        string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Retry          string `json:"retry,omitempty" yaml:"retry,omitempty"`
	CircuitBreaker string `json:"circuitBreaker,omitempty" yaml:"circuitBreaker,omitempty"`
}

type EndpointPolicyNames struct {
	Timeout                 string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Retry                   string `json:"retry,omitempty" yaml:"retry,omitempty"`
	CircuitBreaker          string `json:"circuitBreaker,omitempty" yaml:"circuitBreaker,omitempty"`
	CircuitBreakerCacheSize int    `json:"circuitBreakerCacheSize,omitempty" yaml:"circuitBreakerCacheSize,omitempty"`
}

type ActorPolicyNames struct {
	Timeout                 string `json:"timeout,omitempty" yaml:"timeout,omitempty"`
	Retry                   string `json:"retry,omitempty" yaml:"retry,omitempty"`
	CircuitBreaker          string `json:"circuitBreaker,omitempty" yaml:"circuitBreaker,omitempty"`
	CircuitBreakerScope     string `json:"circuitBreakerScope,omitempty" yaml:"circuitBreakerScope,omitempty"`
	CircuitBreakerCacheSize int    `json:"circuitBreakerCacheSize,omitempty" yaml:"circuitBreakerCacheSize,omitempty"`
}

// ResiliencyList represents a list of `Resiliency` items.
// +kubebuilder:object:root=true
type ResiliencyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Resiliency `json:"items"`
}
