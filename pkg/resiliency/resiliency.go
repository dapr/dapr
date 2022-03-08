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
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	lru "github.com/hashicorp/golang-lru"
	"gopkg.in/yaml.v2"

	resiliency_v1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"

	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/kit/config"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/retry"
)

const (
	operatorRetryCount   = 100
	operatorTimePerRetry = time.Second * 5

	defaultEndpointCacheSize = 100
	defaultActorCacheSize    = 5000
)

// ActorCircuitBreakerScope indicates the scope of the circuit breaker for an actor.
type ActorCircuitBreakerScope int

const (
	// ActorCircuitBreakerScopeType indicates the type scope (less granular).
	ActorCircuitBreakerScopeType ActorCircuitBreakerScope = iota
	// ActorCircuitBreakerScopeID indicates the type+id scope (more granular).
	ActorCircuitBreakerScopeID
	// ActorCircuitBreakerScopeBoth indicates both type and type+id are used for scope.
	ActorCircuitBreakerScopeBoth // Usage is TODO.
)

type (
	// Provider is the interface for returning a `Runner` for the various
	// resiliency scenarios in the runtime.
	Provider interface {
		// EndpointPolicy returns the policy for a service endpoint.
		EndpointPolicy(ctx context.Context, service string, endpoint string) Runner
		// ActorPolicy returns the policy for an actor instance.
		ActorPolicy(ctx context.Context, actorType string, id string) Runner
		// ComponentOutputPolicy returns the output policy for a component.
		ComponentOutputPolicy(ctx context.Context, name string) Runner
		// ComponentInputPolicy returns the input policy for a component.
		ComponentInputPolicy(ctx context.Context, name string) Runner
	}

	// Resiliency encapsulates configuration for timeouts, retries, and circuit breakers.
	// It maps services, actors, components, and routes to each of these configurations.
	// Lastly, it maintains circuit breaker state across invocations.
	Resiliency struct {
		log logger.Logger

		timeouts        map[string]time.Duration
		retries         map[string]*retry.Config
		circuitBreakers map[string]*breaker.CircuitBreaker

		actorCBCaches map[string]*lru.Cache
		serviceCBs    map[string]*lru.Cache
		componentCBs  *circuitBreakerInstances

		apps       map[string]PolicyNames
		actors     map[string]ActorPolicyNames
		components map[string]ComponentPolicyNames
		routes     map[string]PolicyNames
	}

	// circuitBreakerInstances stores circuit breaker state for components
	// that have ephemeral instances (actors, service endpoints).
	circuitBreakerInstances struct {
		sync.RWMutex
		cbs map[string]*breaker.CircuitBreaker
	}

	// ComponentPolicyNames contains the policies for component input and output.
	ComponentPolicyNames struct {
		Input  PolicyNames
		Output PolicyNames
		PolicyNames
	}

	// PolicyNames contains the policy names for a timeout, retry, and circuit breaker.
	// Empty values mean that no policy is configured.
	PolicyNames struct {
		Timeout        string
		Retry          string
		CircuitBreaker string
	}

	// ActorPolicyNames add the circuit breaker scope to PolicyNames.
	ActorPolicyNames struct {
		PolicyNames
		CircuitBreakerScope ActorCircuitBreakerScope
	}
)

// Ensure `*Resiliency` satisfies the `Provider` interface.
var _ = (Provider)((*Resiliency)(nil))

// LoadStandaloneResiliency loads resiliency configurations from a file path.
func LoadStandaloneResiliency(log logger.Logger, runtimeID, path string) []*resiliency_v1alpha.Resiliency {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	files, err := os.ReadDir(path)
	if err != nil {
		log.Errorf("failed to read resiliences from path %s: %s", err)
		return nil
	}

	configs := make([]*resiliency_v1alpha.Resiliency, 0, len(files))

	for _, file := range files {
		filePath := filepath.Join(path, file.Name())
		b, err := os.ReadFile(filePath)
		if err != nil {
			log.Errorf("Could not read resiliency file %s: %w", file.Name(), err)
			continue
		}

		var resiliency resiliency_v1alpha.Resiliency
		if err = yaml.Unmarshal(b, &resiliency); err != nil {
			log.Errorf("Could not parse resiliency file %s: %w", file.Name(), err)
			continue
		}

		configs = append(configs, &resiliency)
	}

	return filterResiliencyConfigs(configs, runtimeID)
}

// LoadKubernetesResiliency loads resiliency configurations from the Kubernetes operator.
func LoadKubernetesResiliency(log logger.Logger, runtimeID, namespace string, operatorClient operatorv1pb.OperatorClient) []*resiliency_v1alpha.Resiliency {
	resp, err := operatorClient.ListResiliency(context.Background(), &operatorv1pb.ListResiliencyRequest{
		Namespace: namespace,
	}, grpc_retry.WithMax(operatorRetryCount), grpc_retry.WithPerRetryTimeout(operatorTimePerRetry))
	if err != nil {
		log.Errorf("Error listing resiliences: %s", err.Error())
		return nil
	}

	if resp.GetResiliencies() == nil {
		log.Debug("No resiliencies found.")
		return nil
	}

	configs := make([]*resiliency_v1alpha.Resiliency, 0, len(resp.GetResiliencies()))

	for _, b := range resp.GetResiliencies() {
		var resiliency resiliency_v1alpha.Resiliency
		if err = yaml.Unmarshal(b, &resiliency); err != nil {
			log.Errorf("Could not parse resiliency: %w", err)
			continue
		}

		configs = append(configs, &resiliency)
	}

	return filterResiliencyConfigs(configs, runtimeID)
}

// FromConfigurations creates a resiliency provider and decodes the configurations from `c`.
func FromConfigurations(log logger.Logger, c ...*resiliency_v1alpha.Resiliency) *Resiliency {
	r := New(log)
	for _, config := range c {
		if err := r.DecodeConfiguration(config); err != nil {
			log.Errorf("Could not read resiliency %s: %w", &config.ObjectMeta.Name, err)
			continue
		}
	}
	return r
}

// New creates a new Resiliency.
func New(log logger.Logger) *Resiliency {
	return &Resiliency{
		log:             log,
		timeouts:        make(map[string]time.Duration),
		retries:         make(map[string]*retry.Config),
		circuitBreakers: make(map[string]*breaker.CircuitBreaker),
		actorCBCaches:   make(map[string]*lru.Cache),
		serviceCBs:      make(map[string]*lru.Cache),
		componentCBs: &circuitBreakerInstances{
			cbs: make(map[string]*breaker.CircuitBreaker, 10),
		},
		apps:       make(map[string]PolicyNames),
		actors:     make(map[string]ActorPolicyNames),
		components: make(map[string]ComponentPolicyNames),
		routes:     make(map[string]PolicyNames),
	}
}

// DecodeConfiguration reads in a single resiliency configuration.
func (r *Resiliency) DecodeConfiguration(c *resiliency_v1alpha.Resiliency) error {
	if c == nil {
		return nil
	}

	if err := r.decodePolicies(c); err != nil {
		return err
	}
	return r.decodeTargets(c)
}

func (r *Resiliency) decodePolicies(c *resiliency_v1alpha.Resiliency) (err error) {
	policies := c.Spec.Policies

	for name, t := range policies.Timeouts {
		if r.timeouts[name], err = parseDuration(t); err != nil {
			return fmt.Errorf("invalid duration %q, %s: %w", name, t, err)
		}
	}

	for name, t := range policies.Retries {
		rc := retry.DefaultConfig()
		m, err := toMap(t)
		if err != nil {
			return err
		}
		if err = retry.DecodeConfig(&rc, m); err != nil {
			return fmt.Errorf("invalid retry configuration %q: %w", name, err)
		}
		r.retries[name] = &rc
	}

	for name, t := range policies.CircuitBreakers {
		var cb breaker.CircuitBreaker
		m, err := toMap(t)
		if err != nil {
			return err
		}
		if err = config.Decode(m, &cb); err != nil {
			return fmt.Errorf("invalid retry configuration %q: %w", name, err)
		}
		cb.Name = name
		cb.Initialize()
		r.circuitBreakers[name] = &cb
	}

	return nil
}

func (r *Resiliency) decodeTargets(c *resiliency_v1alpha.Resiliency) (err error) {
	targets := c.Spec.Targets

	for name, t := range targets.Apps {
		r.apps[name] = PolicyNames{
			Timeout:        t.Timeout,
			Retry:          t.Retry,
			CircuitBreaker: t.CircuitBreaker,
		}
		if t.CircuitBreakerCacheSize == 0 {
			t.CircuitBreakerCacheSize = defaultEndpointCacheSize
		}
		r.serviceCBs[name], err = lru.New(t.CircuitBreakerCacheSize)
		if err != nil {
			return err
		}
	}

	for name, t := range targets.Actors {
		scope, err := ParseActorCircuitBreakerScope(t.CircuitBreakerScope)
		if err != nil {
			return err
		}
		r.actors[name] = ActorPolicyNames{
			PolicyNames: PolicyNames{
				Timeout:        t.Timeout,
				Retry:          t.Retry,
				CircuitBreaker: t.CircuitBreaker,
			},
			CircuitBreakerScope: scope,
		}
		if t.CircuitBreakerCacheSize == 0 {
			t.CircuitBreakerCacheSize = defaultActorCacheSize
		}
		r.actorCBCaches[name], err = lru.New(t.CircuitBreakerCacheSize)
		if err != nil {
			return err
		}
	}

	for name, t := range targets.Components {
		if (t.Input == resiliency_v1alpha.PolicyNames{}) && (t.Output == resiliency_v1alpha.PolicyNames{}) {
			r.components[name] = ComponentPolicyNames{
				Input: PolicyNames{},
				Output: PolicyNames{
					Timeout:        t.Timeout,
					Retry:          t.Retry,
					CircuitBreaker: t.CircuitBreaker,
				},
			}
			continue
		}

		r.components[name] = ComponentPolicyNames{
			Input: PolicyNames{
				Timeout:        t.Input.Timeout,
				Retry:          t.Input.Retry,
				CircuitBreaker: t.Input.CircuitBreaker,
			},
			Output: PolicyNames{
				Timeout:        t.Output.Timeout,
				Retry:          t.Output.Retry,
				CircuitBreaker: t.Output.CircuitBreaker,
			},
		}
	}

	return nil
}

// EndpointPolicy returns the policy for a service endpoint.
func (r *Resiliency) EndpointPolicy(ctx context.Context, app string, endpoint string) Runner {
	var t time.Duration
	var rc *retry.Config
	var cb *breaker.CircuitBreaker
	operationName := fmt.Sprintf("endpoint[%s, %s]", app, endpoint)
	if r == nil {
		return Policy(ctx, r.log, operationName, t, rc, cb)
	}
	policyNames, ok := r.apps[app]
	if ok {
		if policyNames.Timeout != "" {
			t = r.timeouts[policyNames.Timeout]
		}
		if policyNames.Retry != "" {
			rc = r.retries[policyNames.Retry]
		}
		if policyNames.CircuitBreaker != "" {
			template := r.circuitBreakers[policyNames.CircuitBreaker]
			if ok {
				cache, ok := r.serviceCBs[app]
				if ok {
					cbi, ok := cache.Get(endpoint)
					if ok {
						cb, _ = cbi.(*breaker.CircuitBreaker)
					} else {
						cb = &breaker.CircuitBreaker{
							Name:        endpoint,
							MaxRequests: template.MaxRequests,
							Interval:    template.Interval,
							Timeout:     template.Timeout,
							Trip:        template.Trip,
						}
						cb.Initialize()
						cache.Add(endpoint, cb)
					}
				}
			}
		}
	}

	return Policy(ctx, r.log, operationName, t, rc, cb)
}

// ActorPolicy returns the policy for an actor instance.
func (r *Resiliency) ActorPolicy(ctx context.Context, actorType string, id string) Runner {
	var t time.Duration
	var rc *retry.Config
	var cb *breaker.CircuitBreaker
	operationName := fmt.Sprintf("actor[%s, %s]", actorType, id)
	if r == nil {
		return Policy(ctx, r.log, operationName, t, rc, cb)
	}
	policyNames, ok := r.actors[actorType]
	if ok {
		if policyNames.Timeout != "" {
			t = r.timeouts[policyNames.Timeout]
		}
		if policyNames.Retry != "" {
			rc = r.retries[policyNames.Retry]
		}
		if policyNames.CircuitBreaker != "" {
			template, ok := r.circuitBreakers[policyNames.CircuitBreaker]
			if ok {
				cache, ok := r.actorCBCaches[actorType]
				if ok {
					var key string
					if policyNames.CircuitBreakerScope == ActorCircuitBreakerScopeType {
						key = actorType
					} else {
						key = actorType + "-" + id
					}

					cbi, ok := cache.Get(key)
					if ok {
						cb, _ = cbi.(*breaker.CircuitBreaker)
					} else {
						cb = &breaker.CircuitBreaker{
							Name:        key,
							MaxRequests: template.MaxRequests,
							Interval:    template.Interval,
							Timeout:     template.Timeout,
							Trip:        template.Trip,
						}
						cb.Initialize()
						cache.Add(key, cb)
					}
				}
			}
		}
	}

	return Policy(ctx, r.log, operationName, t, rc, cb)
}

// ComponentPolicy returns the output policy for a component.
func (r *Resiliency) ComponentOutputPolicy(ctx context.Context, name string) Runner {
	var t time.Duration
	var rc *retry.Config
	var cb *breaker.CircuitBreaker
	operationName := fmt.Sprintf("component[%s] output", name)
	if r == nil {
		return Policy(ctx, r.log, operationName, t, rc, cb)
	}
	componentPolicies, ok := r.components[name]
	if ok {
		if componentPolicies.Output.Timeout != "" {
			t = r.timeouts[componentPolicies.Output.Timeout]
		}
		if componentPolicies.Output.Retry != "" {
			rc = r.retries[componentPolicies.Output.Retry]
		}
		if componentPolicies.Output.CircuitBreaker != "" {
			template := r.circuitBreakers[componentPolicies.Output.CircuitBreaker]
			cb = r.componentCBs.Get(name, template)
		}
	}

	return Policy(ctx, r.log, operationName, t, rc, cb)
}

// ComponentPolicy returns the policy for a component.
func (r *Resiliency) ComponentInputPolicy(ctx context.Context, name string) Runner {
	var t time.Duration
	var rc *retry.Config
	var cb *breaker.CircuitBreaker
	operationName := fmt.Sprintf("component[%s] input", name)
	if r == nil {
		return Policy(ctx, r.log, operationName, t, rc, cb)
	}
	componentPolicies, ok := r.components[name]
	if ok {
		if componentPolicies.Input.Timeout != "" {
			t = r.timeouts[componentPolicies.Input.Timeout]
		}
		if componentPolicies.Input.Retry != "" {
			rc = r.retries[componentPolicies.Input.Retry]
		}
		if componentPolicies.Input.CircuitBreaker != "" {
			template := r.circuitBreakers[componentPolicies.Input.CircuitBreaker]
			cb = r.componentCBs.Get(name, template)
		}
	}

	return Policy(ctx, r.log, operationName, t, rc, cb)
}

// Get returns a cached circuit breaker if one exists.
// Otherwise, it returns a new circuit breaker based on the provided template.
func (e *circuitBreakerInstances) Get(instanceName string, template *breaker.CircuitBreaker) *breaker.CircuitBreaker {
	e.RLock()
	cb, ok := e.cbs[instanceName]
	e.RUnlock()
	if ok {
		return cb
	}

	cb = &breaker.CircuitBreaker{
		Name:        template.Name + "-" + instanceName,
		MaxRequests: template.MaxRequests,
		Interval:    template.Interval,
		Timeout:     template.Timeout,
		Trip:        template.Trip,
	}
	cb.Initialize()

	e.Lock()
	e.cbs[instanceName] = cb
	e.Unlock()

	return cb
}

// Remove deletes a circuit break from the cache.
func (e *circuitBreakerInstances) Remove(name string) {
	e.Lock()
	delete(e.cbs, name)
	e.Unlock()
}

func toMap(val interface{}) (interface{}, error) {
	jsonBytes, err := json.Marshal(val)
	if err != nil {
		return nil, err
	}
	var v interface{}
	err = json.Unmarshal(jsonBytes, &v)

	return v, err
}

func parseDuration(val string) (time.Duration, error) {
	if i, err := strconv.ParseInt(val, 10, 64); err == nil {
		return time.Duration(i) * time.Millisecond, nil
	}
	return time.ParseDuration(val)
}

// ParseActorCircuitBreakerScope parses a string to a `ActorCircuitBreakerScope`.
func ParseActorCircuitBreakerScope(val string) (ActorCircuitBreakerScope, error) {
	switch val {
	case "type":
		return ActorCircuitBreakerScopeType, nil
	case "id":
		return ActorCircuitBreakerScopeID, nil
	case "both":
		return ActorCircuitBreakerScopeBoth, nil
	}
	return ActorCircuitBreakerScope(0), fmt.Errorf("unknown circuit breaker scope %q", val)
}

func filterResiliencyConfigs(resiliences []*resiliency_v1alpha.Resiliency, runtimeID string) []*resiliency_v1alpha.Resiliency {
	var filteredResiliencies []*resiliency_v1alpha.Resiliency

	for _, resiliency := range resiliences {
		if len(resiliency.Scopes) == 0 {
			filteredResiliencies = append(filteredResiliencies, resiliency)
			continue
		}

		for _, scope := range resiliency.Scopes {
			if scope == runtimeID {
				filteredResiliencies = append(filteredResiliencies, resiliency)
				break
			}
		}
	}

	return filteredResiliencies
}
