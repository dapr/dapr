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
		// ActorPolicy returns the policy for an actor instance to be used before the lock is acquired.
		ActorPreLockPolicy(ctx context.Context, actorType string, id string) Runner
		// ActorPolicy returns the policy for an actor instance to be used after the lock is acquired.
		ActorPostLockPolicy(ctx context.Context, actorType string, id string) Runner
		// ComponentOutboundPolicy returns the outbound policy for a component.
		ComponentOutboundPolicy(ctx context.Context, name string) Runner
		// ComponentInboundPolicy returns the inbound policy for a component.
		ComponentInboundPolicy(ctx context.Context, name string) Runner
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
		actors     map[string]ActorPolicies
		components map[string]ComponentPolicyNames
	}

	// circuitBreakerInstances stores circuit breaker state for components
	// that have ephemeral instances (actors, service endpoints).
	circuitBreakerInstances struct {
		sync.RWMutex
		cbs map[string]*breaker.CircuitBreaker
	}

	// ComponentPolicyNames contains the policies for component input and output.
	ComponentPolicyNames struct {
		Inbound  PolicyNames
		Outbound PolicyNames
	}

	// PolicyNames contains the policy names for a timeout, retry, and circuit breaker.
	// Empty values mean that no policy is configured.
	PolicyNames struct {
		Timeout        string
		Retry          string
		CircuitBreaker string
	}

	// Actors have different behavior before and after locking.
	ActorPolicies struct {
		PreLockPolicies  ActorPreLockPolicyNames
		PostLockPolicies ActorPostLockPolicyNames
	}

	// Policy used before an actor is locked. It does not include a timeout as we want to wait forever for the actor.
	ActorPreLockPolicyNames struct {
		Retry               string
		CircuitBreaker      string
		CircuitBreakerScope ActorCircuitBreakerScope
	}

	// Policy used after an actor is locked. It only uses timeout as retry/circuit breaker is handled before locking.
	ActorPostLockPolicyNames struct {
		Timeout string
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
		actors:     make(map[string]ActorPolicies),
		components: make(map[string]ComponentPolicyNames),
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
		cb.Initialize(r.log)
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
		if t.CircuitBreakerScope == "" && t.CircuitBreaker != "" {
			return fmt.Errorf("actor circuit breakers must include scope")
		}

		if t.CircuitBreaker != "" {
			scope, cErr := ParseActorCircuitBreakerScope(t.CircuitBreakerScope)
			if cErr != nil {
				return cErr
			}

			r.actors[name] = ActorPolicies{
				PreLockPolicies: ActorPreLockPolicyNames{
					Retry:               t.Retry,
					CircuitBreaker:      t.CircuitBreaker,
					CircuitBreakerScope: scope,
				},
				PostLockPolicies: ActorPostLockPolicyNames{
					Timeout: t.Timeout,
				},
			}
		} else {
			r.actors[name] = ActorPolicies{
				PreLockPolicies: ActorPreLockPolicyNames{
					Retry:          t.Retry,
					CircuitBreaker: "",
				},
				PostLockPolicies: ActorPostLockPolicyNames{
					Timeout: t.Timeout,
				},
			}
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
		r.components[name] = ComponentPolicyNames{
			Inbound: PolicyNames{
				Timeout:        t.Inbound.Timeout,
				Retry:          t.Inbound.Retry,
				CircuitBreaker: t.Inbound.CircuitBreaker,
			},
			Outbound: PolicyNames{
				Timeout:        t.Outbound.Timeout,
				Retry:          t.Outbound.Retry,
				CircuitBreaker: t.Outbound.CircuitBreaker,
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
						cb.Initialize(r.log)
						cache.Add(endpoint, cb)
					}
				}
			}
		}
	}

	return Policy(ctx, r.log, operationName, t, rc, cb)
}

// ActorPreLockPolicy returns the policy for an actor instance to be used before an actor lock is acquired.
func (r *Resiliency) ActorPreLockPolicy(ctx context.Context, actorType string, id string) Runner {
	var t time.Duration
	var rc *retry.Config
	var cb *breaker.CircuitBreaker
	operationName := fmt.Sprintf("actor[%s, %s]", actorType, id)
	if r == nil {
		return Policy(ctx, r.log, operationName, t, rc, cb)
	}
	actorPolicies, ok := r.actors[actorType]
	if policyNames := actorPolicies.PreLockPolicies; ok {
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
						cb.Initialize(r.log)
						cache.Add(key, cb)
					}
				}
			}
		}
	}

	return Policy(ctx, r.log, operationName, t, rc, cb)
}

// ActorPostLockPolicy returns the policy for an actor instance to be used after an actor lock is acquired.
func (r *Resiliency) ActorPostLockPolicy(ctx context.Context, actorType string, id string) Runner {
	var t time.Duration
	var rc *retry.Config
	var cb *breaker.CircuitBreaker
	operationName := fmt.Sprintf("actor[%s, %s]", actorType, id)
	if r == nil {
		return Policy(ctx, r.log, operationName, t, rc, cb)
	}
	actorPolicies, ok := r.actors[actorType]
	if policyNames := actorPolicies.PostLockPolicies; ok {
		if policyNames.Timeout != "" {
			t = r.timeouts[policyNames.Timeout]
		}
	}

	return Policy(ctx, r.log, operationName, t, rc, cb)
}

// ComponentPolicy returns the output policy for a component.
func (r *Resiliency) ComponentOutboundPolicy(ctx context.Context, name string) Runner {
	var t time.Duration
	var rc *retry.Config
	var cb *breaker.CircuitBreaker
	operationName := fmt.Sprintf("component[%s] output", name)
	if r == nil {
		return Policy(ctx, r.log, operationName, t, rc, cb)
	}
	componentPolicies, ok := r.components[name]
	if ok {
		if componentPolicies.Outbound.Timeout != "" {
			t = r.timeouts[componentPolicies.Outbound.Timeout]
		}
		if componentPolicies.Outbound.Retry != "" {
			rc = r.retries[componentPolicies.Outbound.Retry]
		}
		if componentPolicies.Outbound.CircuitBreaker != "" {
			template := r.circuitBreakers[componentPolicies.Outbound.CircuitBreaker]
			cb = r.componentCBs.Get(r.log, name, template)
		}
	}

	return Policy(ctx, r.log, operationName, t, rc, cb)
}

// ComponentPolicy returns the policy for a component.
func (r *Resiliency) ComponentInboundPolicy(ctx context.Context, name string) Runner {
	var t time.Duration
	var rc *retry.Config
	var cb *breaker.CircuitBreaker
	operationName := fmt.Sprintf("component[%s] input", name)
	if r == nil {
		return Policy(ctx, r.log, operationName, t, rc, cb)
	}
	componentPolicies, ok := r.components[name]
	if ok {
		if componentPolicies.Inbound.Timeout != "" {
			t = r.timeouts[componentPolicies.Inbound.Timeout]
		}
		if componentPolicies.Inbound.Retry != "" {
			rc = r.retries[componentPolicies.Inbound.Retry]
		}
		if componentPolicies.Inbound.CircuitBreaker != "" {
			template := r.circuitBreakers[componentPolicies.Inbound.CircuitBreaker]
			cb = r.componentCBs.Get(r.log, name, template)
		}
	}

	return Policy(ctx, r.log, operationName, t, rc, cb)
}

// Get returns a cached circuit breaker if one exists.
// Otherwise, it returns a new circuit breaker based on the provided template.
func (e *circuitBreakerInstances) Get(log logger.Logger, instanceName string, template *breaker.CircuitBreaker) *breaker.CircuitBreaker {
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
	cb.Initialize(log)

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
