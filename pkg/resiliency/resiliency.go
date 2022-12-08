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

	"github.com/dapr/dapr/utils"

	"github.com/ghodss/yaml"
	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	lru "github.com/hashicorp/golang-lru"

	diag "github.com/dapr/dapr/pkg/diagnostics"

	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	BuiltInServiceRetries         BuiltInPolicyName     = "DaprBuiltInServiceRetries"
	BuiltInActorRetries           BuiltInPolicyName     = "DaprBuiltInActorRetries"
	BuiltInActorReminderRetries   BuiltInPolicyName     = "DaprBuiltInActorReminderRetries"
	BuiltInActorNotFoundRetries   BuiltInPolicyName     = "DaprBuiltInActorNotFoundRetries"
	BuiltInInitializationRetries  BuiltInPolicyName     = "DaprBuiltInInitializationRetries"
	DefaultRetryTemplate          DefaultPolicyTemplate = "Default%sRetryPolicy"
	DefaultTimeoutTemplate        DefaultPolicyTemplate = "Default%sTimeoutPolicy"
	DefaultCircuitBreakerTemplate DefaultPolicyTemplate = "Default%sCircuitBreakerPolicy"
	Endpoint                      PolicyTypeName        = "App"
	Component                     PolicyTypeName        = "Component"
	Actor                         PolicyTypeName        = "Actor"
	Binding                       ComponentType         = "Binding"
	Configuration                 ComponentType         = "Configuration"
	Lock                          ComponentType         = "Lock"
	Pubsub                        ComponentType         = "Pubsub"
	Secretstore                   ComponentType         = "Secretstore"
	Statestore                    ComponentType         = "Statestore"
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

	resiliencyKind = "Resiliency"
)

type (
	// Provider is the interface for returning a `*PolicyDefinition` for the various
	// resiliency scenarios in the runtime.
	Provider interface {
		// EndpointPolicy returns the policy for a service endpoint.
		EndpointPolicy(service string, endpoint string) *PolicyDefinition
		// ActorPolicy returns the policy for an actor instance to be used before the lock is acquired.
		ActorPreLockPolicy(actorType string, id string) *PolicyDefinition
		// ActorPolicy returns the policy for an actor instance to be used after the lock is acquired.
		ActorPostLockPolicy(actorType string, id string) *PolicyDefinition
		// ComponentOutboundPolicy returns the outbound policy for a component.
		ComponentOutboundPolicy(name string, componentType ComponentType) *PolicyDefinition
		// ComponentInboundPolicy returns the inbound policy for a component.
		ComponentInboundPolicy(name string, componentType ComponentType) *PolicyDefinition
		// BuiltInPolicy are used to replace existing retries in Dapr which may not bind specifically to one of the above categories.
		BuiltInPolicy(name BuiltInPolicyName) *PolicyDefinition
		// GetPolicy returns the policy that applies to the target, or nil if there is none.
		GetPolicy(target string, policyType PolicyType) *PolicyDescription
	}

	// Resiliency encapsulates configuration for timeouts, retries, and circuit breakers.
	// It maps services, actors, components, and routes to each of these configurations.
	// Lastly, it maintains circuit breaker state across invocations.
	Resiliency struct {
		name      string
		namespace string
		log       logger.Logger

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

	// PolicyDescription contains the policies that are applied to a target.
	PolicyDescription struct {
		RetryPolicy    *retry.Config
		TimeoutPolicy  time.Duration
		CircuitBreaker *breaker.CircuitBreaker
	}

	DefaultPolicyTemplate string
	BuiltInPolicyName     string
	PolicyTypeName        string

	// PolicyTypes have to return an array of their possible levels.
	// Ex. [App], [Actor], [Component, Inbound|Outbound, ComponentType]
	PolicyType interface {
		getPolicyLevels() []string
		getPolicyTypeName() PolicyTypeName
	}
	EndpointPolicy  struct{}
	ActorPolicy     struct{}
	ComponentType   string
	ComponentPolicy struct {
		componentType      ComponentType
		componentDirection string
	}
)

// Ensure `*Resiliency` satisfies the `Provider` interface.
var _ = (Provider)((*Resiliency)(nil))

// LoadStandaloneResiliency loads resiliency configurations from a file path.
func LoadStandaloneResiliency(log logger.Logger, runtimeID, path string) []*resiliencyV1alpha.Resiliency {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}

	files, err := os.ReadDir(path)
	if err != nil {
		log.Errorf("failed to read resiliences from path %s: %s", path, err)
		return nil
	}

	configs := make([]*resiliencyV1alpha.Resiliency, 0, len(files))

	type typeInfo struct {
		metav1.TypeMeta `json:",inline"`
	}

	for _, file := range files {
		if !utils.IsYaml(file.Name()) {
			log.Warnf("A non-YAML resiliency file %s was detected, it will not be loaded", file.Name())
			continue
		}
		filePath := filepath.Join(path, file.Name())
		b, err := os.ReadFile(filePath)
		if err != nil {
			log.Errorf("Could not read resiliency file %s: %w", file.Name(), err)
			continue
		}

		var ti typeInfo
		if err = yaml.Unmarshal(b, &ti); err != nil {
			log.Errorf("Could not determine resource type: %s", err.Error())
			continue
		}

		if ti.Kind != resiliencyKind {
			continue
		}

		var resiliency resiliencyV1alpha.Resiliency
		if err = yaml.Unmarshal(b, &resiliency); err != nil {
			log.Errorf("Could not parse resiliency file %s: %w", file.Name(), err)
			continue
		}
		configs = append(configs, &resiliency)
	}

	return filterResiliencyConfigs(configs, runtimeID)
}

// LoadKubernetesResiliency loads resiliency configurations from the Kubernetes operator.
func LoadKubernetesResiliency(log logger.Logger, runtimeID, namespace string, operatorClient operatorv1pb.OperatorClient) []*resiliencyV1alpha.Resiliency {
	resp, err := operatorClient.ListResiliency(context.Background(), &operatorv1pb.ListResiliencyRequest{
		Namespace: namespace,
	}, grpcRetry.WithMax(operatorRetryCount), grpcRetry.WithPerRetryTimeout(operatorTimePerRetry))
	if err != nil {
		log.Errorf("Error listing resiliences: %s", err.Error())
		return nil
	}

	if resp.GetResiliencies() == nil {
		log.Debug("No resiliencies found.")
		return nil
	}

	configs := make([]*resiliencyV1alpha.Resiliency, 0, len(resp.GetResiliencies()))

	for _, b := range resp.GetResiliencies() {
		var resiliency resiliencyV1alpha.Resiliency
		if err = yaml.Unmarshal(b, &resiliency); err != nil {
			log.Errorf("Could not parse resiliency: %w", err)
			continue
		}

		configs = append(configs, &resiliency)
	}

	return filterResiliencyConfigs(configs, runtimeID)
}

// FromConfigurations creates a resiliency provider and decodes the configurations from `c`.
func FromConfigurations(log logger.Logger, c ...*resiliencyV1alpha.Resiliency) *Resiliency {
	r := New(log)

	// Add the default policies into the overall resiliency first. This allows customers to overwrite them if desired.
	r.addBuiltInPolicies()

	for _, config := range c {
		log.Infof("Loading Resiliency configuration: %s", config.Name)
		log.Debugf("Resiliency configuration (%s): %+v", config.Name, config)
		if err := r.DecodeConfiguration(config); err != nil {
			log.Errorf("Could not read resiliency %s: %w", &config.ObjectMeta.Name, err)
			continue
		}
		diag.DefaultResiliencyMonitoring.PolicyLoaded(config.Name, config.Namespace)
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
func (r *Resiliency) DecodeConfiguration(c *resiliencyV1alpha.Resiliency) error {
	if c == nil {
		return nil
	}
	r.name = c.Name
	r.namespace = c.Namespace

	if err := r.decodePolicies(c); err != nil {
		return err
	}
	return r.decodeTargets(c)
}

// Adds policies that cover the existing retries in Dapr like service invocation.
func (r *Resiliency) addBuiltInPolicies() {
	// Cover retries for remote service invocation, but don't overwrite anything that is already present.
	if _, ok := r.retries[string(BuiltInServiceRetries)]; !ok {
		r.retries[string(BuiltInServiceRetries)] = &retry.Config{
			Policy: retry.PolicyConstant,
			// Note: If this value changes to 0, don't forget to disable "Replay" in direct messaging
			MaxRetries: 3,
			Duration:   time.Second,
		}
	}

	// Cover retries for remote actor invocation, but don't overwrite anything that is already present.
	if _, ok := r.retries[string(BuiltInActorRetries)]; !ok {
		r.retries[string(BuiltInActorRetries)] = &retry.Config{
			Policy:     retry.PolicyConstant,
			MaxRetries: 3,
			Duration:   time.Second,
		}
	}

	// Cover retries for actor reminder operations, but don't overwrite anything that is already present.
	if _, ok := r.retries[string(BuiltInActorReminderRetries)]; !ok {
		r.retries[string(BuiltInActorReminderRetries)] = &retry.Config{
			Policy:              retry.PolicyExponential,
			InitialInterval:     500 * time.Millisecond,
			RandomizationFactor: 0.5,
			Multiplier:          1.5,
			MaxInterval:         60 * time.Second,
			MaxElapsedTime:      15 * time.Minute,
		}
	}

	// Cover retries for initialization, but don't overwrite anything that is already present.
	if _, ok := r.retries[string(BuiltInInitializationRetries)]; !ok {
		r.retries[string(BuiltInInitializationRetries)] = &retry.Config{
			Policy:              retry.PolicyExponential,
			InitialInterval:     time.Millisecond * 500,
			MaxRetries:          3,
			MaxInterval:         time.Second,
			MaxElapsedTime:      time.Second * 10,
			Duration:            time.Second * 2,
			Multiplier:          1.5,
			RandomizationFactor: 0.5,
		}
	}

	if _, ok := r.retries[string(BuiltInActorNotFoundRetries)]; !ok {
		r.retries[string(BuiltInActorNotFoundRetries)] = &retry.Config{
			Policy:     retry.PolicyConstant,
			MaxRetries: 5,
			Duration:   time.Second,
		}
	}
}

func (r *Resiliency) decodePolicies(c *resiliencyV1alpha.Resiliency) (err error) {
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

		if !r.isProtectedPolicy(name) {
			if r.isBuiltInPolicy(name) && rc.MaxRetries < 3 {
				r.log.Warnf("Attempted override of %s did not meet minimum retry count, resetting to 3.", name)
				rc.MaxRetries = 3
			}

			r.retries[name] = &rc
		} else {
			r.log.Warnf("Attempted to override protected policy %s which is not allowed. Ignoring provided policy and using default.", name)
		}
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

func (r *Resiliency) decodeTargets(c *resiliencyV1alpha.Resiliency) (err error) {
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

func (r *Resiliency) isBuiltInPolicy(name string) bool {
	switch name {
	case string(BuiltInServiceRetries):
		fallthrough
	case string(BuiltInActorRetries):
		fallthrough
	case string(BuiltInActorReminderRetries):
		fallthrough
	case string(BuiltInInitializationRetries):
		fallthrough
	case string(BuiltInActorNotFoundRetries):
		return true
	default:
		return false
	}
}

func (r *Resiliency) isProtectedPolicy(name string) bool {
	switch name {
	case string(BuiltInActorNotFoundRetries):
		return true
	default:
		return false
	}
}

// EndpointPolicy returns the policy for a service endpoint.
func (r *Resiliency) EndpointPolicy(app string, endpoint string) *PolicyDefinition {
	policyDef := &PolicyDefinition{
		log:  r.log,
		name: "endpoint[" + app + ", " + endpoint + "]",
	}
	policyNames, ok := r.apps[app]
	if ok {
		r.log.Debugf("Found Endpoint Policy for %s: %+v", app, policyNames)
		if policyNames.Timeout != "" {
			policyDef.t = r.timeouts[policyNames.Timeout]
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.TimeoutPolicy)
		}
		if policyNames.Retry != "" {
			policyDef.r = r.retries[policyNames.Retry]
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.RetryPolicy)
		}
		if policyNames.CircuitBreaker != "" {
			template, ok := r.circuitBreakers[policyNames.CircuitBreaker]
			if ok {
				cache, ok := r.serviceCBs[app]
				if ok {
					cbi, ok := cache.Get(endpoint)
					if ok {
						policyDef.cb, _ = cbi.(*breaker.CircuitBreaker)
					} else {
						policyDef.cb = &breaker.CircuitBreaker{
							Name:        endpoint,
							MaxRequests: template.MaxRequests,
							Interval:    template.Interval,
							Timeout:     template.Timeout,
							Trip:        template.Trip,
						}
						policyDef.cb.Initialize(r.log)
						cache.Add(endpoint, policyDef.cb)
					}
				}
			}
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.CircuitBreakerPolicy)
		}
	} else {
		if defaultNames, ok := r.getDefaultPolicy(&EndpointPolicy{}); ok {
			r.log.Debugf("Found Default Policy for Endpoint %s: %+v", app, defaultNames)
			if defaultNames.Retry != "" {
				policyDef.r = r.retries[defaultNames.Retry]
			}
			if defaultNames.Timeout != "" {
				policyDef.t = r.timeouts[defaultNames.Timeout]
			}

			if defaultNames.CircuitBreaker != "" {
				template, ok := r.circuitBreakers[defaultNames.CircuitBreaker]
				if ok {
					cache, ok := r.serviceCBs[app]
					if ok {
						cbi, ok := cache.Get(endpoint)
						if ok {
							policyDef.cb, _ = cbi.(*breaker.CircuitBreaker)
						} else {
							policyDef.cb = &breaker.CircuitBreaker{
								Name:        endpoint,
								MaxRequests: template.MaxRequests,
								Interval:    template.Interval,
								Timeout:     template.Timeout,
								Trip:        template.Trip,
							}
							policyDef.cb.Initialize(r.log)
							cache.Add(endpoint, policyDef.cb)
						}
					}
					diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.CircuitBreakerPolicy)
				}
			}
		}
	}

	return policyDef
}

// ActorPreLockPolicy returns the policy for an actor instance to be used before an actor lock is acquired.
func (r *Resiliency) ActorPreLockPolicy(actorType string, id string) *PolicyDefinition {
	policyDef := &PolicyDefinition{
		log:  r.log,
		name: "actor[" + actorType + ", " + id + "]",
	}
	actorPolicies, ok := r.actors[actorType]
	if policyNames := actorPolicies.PreLockPolicies; ok {
		r.log.Debugf("Found Actor Policy for type %s: %+v", actorType, policyNames)
		if policyNames.Retry != "" {
			policyDef.r = r.retries[policyNames.Retry]
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.RetryPolicy)
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
						policyDef.cb, _ = cbi.(*breaker.CircuitBreaker)
					} else {
						policyDef.cb = &breaker.CircuitBreaker{
							Name:        key,
							MaxRequests: template.MaxRequests,
							Interval:    template.Interval,
							Timeout:     template.Timeout,
							Trip:        template.Trip,
						}
						policyDef.cb.Initialize(r.log)
						cache.Add(key, policyDef.cb)
					}
				}
			}
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.CircuitBreakerPolicy)
		}
	} else {
		if defaultNames, ok := r.getDefaultPolicy(&ActorPolicy{}); ok {
			r.log.Debugf("Found Default Policy for Actor type %s: %+v", actorType, defaultNames)
			if defaultNames.Retry != "" {
				policyDef.r = r.retries[defaultNames.Retry]
			}

			if defaultNames.CircuitBreaker != "" {
				template, ok := r.circuitBreakers[defaultNames.CircuitBreaker]
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
							policyDef.cb, _ = cbi.(*breaker.CircuitBreaker)
						} else {
							policyDef.cb = &breaker.CircuitBreaker{
								Name:        key,
								MaxRequests: template.MaxRequests,
								Interval:    template.Interval,
								Timeout:     template.Timeout,
								Trip:        template.Trip,
							}
							policyDef.cb.Initialize(r.log)
							cache.Add(key, policyDef.cb)
						}
					}
					diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.CircuitBreakerPolicy)
				}
			}
		}
	}

	return policyDef
}

// ActorPostLockPolicy returns the policy for an actor instance to be used after an actor lock is acquired.
func (r *Resiliency) ActorPostLockPolicy(actorType string, id string) *PolicyDefinition {
	policyDef := &PolicyDefinition{
		log:  r.log,
		name: "actor[" + actorType + ", " + id + "]",
	}
	actorPolicies, ok := r.actors[actorType]
	if policyNames := actorPolicies.PostLockPolicies; ok {
		r.log.Debugf("Found Actor Policy for type %s: %+v", actorType, policyNames)
		if policyNames.Timeout != "" {
			policyDef.t = r.timeouts[policyNames.Timeout]
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.TimeoutPolicy)
		}
	} else {
		if defaultPolicies, ok := r.getDefaultPolicy(&ActorPolicy{}); ok {
			r.log.Debugf("Found Default Policy for Actor type %s: %+v", actorType, defaultPolicies)
			if defaultPolicies.Timeout != "" {
				policyDef.t = r.timeouts[defaultPolicies.Timeout]
			}
		}
	}

	return policyDef
}

// ComponentOutboundPolicy returns the outbound policy for a component.
func (r *Resiliency) ComponentOutboundPolicy(name string, componentType ComponentType) *PolicyDefinition {
	policyDef := &PolicyDefinition{
		log:  r.log,
		name: "component[" + name + "] output",
	}
	componentPolicies, ok := r.components[name]
	if ok {
		r.log.Debugf("Found Component Outbound Policy for component %s: %+v", name, componentPolicies.Outbound)
		if componentPolicies.Outbound.Timeout != "" {
			policyDef.t = r.timeouts[componentPolicies.Outbound.Timeout]
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.TimeoutPolicy)
		}
		if componentPolicies.Outbound.Retry != "" {
			policyDef.r = r.retries[componentPolicies.Outbound.Retry]
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.RetryPolicy)
		}
		if componentPolicies.Outbound.CircuitBreaker != "" {
			template := r.circuitBreakers[componentPolicies.Outbound.CircuitBreaker]
			policyDef.cb = r.componentCBs.Get(r.log, name, template)
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.CircuitBreakerPolicy)
		}
	} else {
		if defaultPolicies, ok := r.getDefaultPolicy(&ComponentPolicy{componentType: componentType, componentDirection: "Outbound"}); ok {
			r.log.Debugf("Found Default Policy for Component: %s: %+v", name, defaultPolicies)
			if defaultPolicies.Timeout != "" {
				policyDef.t = r.timeouts[defaultPolicies.Timeout]
			}
			if defaultPolicies.Retry != "" {
				policyDef.r = r.retries[defaultPolicies.Retry]
			}
			if defaultPolicies.CircuitBreaker != "" {
				template := r.circuitBreakers[defaultPolicies.CircuitBreaker]
				policyDef.cb = r.componentCBs.Get(r.log, name, template)
			}
		}
	}

	return policyDef
}

// ComponentInboundPolicy returns the inbound policy for a component.
func (r *Resiliency) ComponentInboundPolicy(name string, componentType ComponentType) *PolicyDefinition {
	policyDef := &PolicyDefinition{
		log:  r.log,
		name: "component[" + name + "] input",
	}
	componentPolicies, ok := r.components[name]
	if ok {
		r.log.Debugf("Found Component Inbound Policy for component %s: %+v", name, componentPolicies.Inbound)
		if componentPolicies.Inbound.Timeout != "" {
			policyDef.t = r.timeouts[componentPolicies.Inbound.Timeout]
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.TimeoutPolicy)
		}
		if componentPolicies.Inbound.Retry != "" {
			policyDef.r = r.retries[componentPolicies.Inbound.Retry]
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.RetryPolicy)
		}
		if componentPolicies.Inbound.CircuitBreaker != "" {
			template := r.circuitBreakers[componentPolicies.Inbound.CircuitBreaker]
			policyDef.cb = r.componentCBs.Get(r.log, name, template)
			diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.CircuitBreakerPolicy)
		}
	} else {
		if defaultPolicies, ok := r.getDefaultPolicy(&ComponentPolicy{componentType: componentType, componentDirection: "Inbound"}); ok {
			r.log.Debugf("Found Default Policy for Component: %s: %+v", name, defaultPolicies)
			if defaultPolicies.Timeout != "" {
				policyDef.t = r.timeouts[defaultPolicies.Timeout]
			}
			if defaultPolicies.Retry != "" {
				policyDef.r = r.retries[defaultPolicies.Retry]
			}
			if defaultPolicies.CircuitBreaker != "" {
				template := r.circuitBreakers[defaultPolicies.CircuitBreaker]
				policyDef.cb = r.componentCBs.Get(r.log, name, template)
			}
		}
	}

	return policyDef
}

// BuiltInPolicy returns a policy that represents a specific built-in retry scenario.
func (r *Resiliency) BuiltInPolicy(name BuiltInPolicyName) *PolicyDefinition {
	nameStr := string(name)
	return &PolicyDefinition{
		log:  r.log,
		name: nameStr,
		r:    r.retries[nameStr],
	}
}

// GetPolicy returns the policy that applies to the target, or nil if there is none.
func (r *Resiliency) GetPolicy(target string, policyType PolicyType) *PolicyDescription {
	var (
		policyName PolicyNames
		exists     bool
	)
	switch policyType.getPolicyTypeName() {
	case Endpoint:
		policyName, exists = r.apps[target]
	case Component:
		var componentPolicy ComponentPolicyNames
		componentPolicy, exists = r.components[target]
		if exists {
			policy, _ := policyType.(*ComponentPolicy)
			if policy.componentDirection == "Inbound" {
				policyName = PolicyNames{
					Retry:          componentPolicy.Inbound.Retry,
					CircuitBreaker: componentPolicy.Inbound.CircuitBreaker,
					Timeout:        componentPolicy.Inbound.Timeout,
				}
			} else {
				policyName = PolicyNames{
					Retry:          componentPolicy.Outbound.Retry,
					CircuitBreaker: componentPolicy.Outbound.CircuitBreaker,
					Timeout:        componentPolicy.Outbound.Timeout,
				}
			}
		}
	case Actor:
		var actorPolicyName ActorPolicies
		actorPolicyName, exists = r.actors[target]
		if exists {
			policyName = PolicyNames{
				Retry:          actorPolicyName.PreLockPolicies.Retry,
				CircuitBreaker: actorPolicyName.PreLockPolicies.CircuitBreaker,
				Timeout:        actorPolicyName.PostLockPolicies.Timeout,
			}
		}
	}

	if !exists {
		return nil
	}
	return r.policyDescription(policyName)
}

func (r *Resiliency) policyDescription(policyName PolicyNames) *PolicyDescription {
	obj := &PolicyDescription{}
	if policyName.Retry != "" && r.retries[policyName.Retry] != nil {
		obj.RetryPolicy = r.retries[policyName.Retry]
	}
	if policyName.CircuitBreaker != "" && r.circuitBreakers[policyName.CircuitBreaker] != nil {
		obj.CircuitBreaker = r.circuitBreakers[policyName.CircuitBreaker]
	}
	if policyName.Timeout != "" && r.timeouts[policyName.Timeout] > 0 {
		obj.TimeoutPolicy = r.timeouts[policyName.Timeout]
	}
	return obj
}

func (r *Resiliency) getDefaultPolicy(policyType PolicyType) (PolicyNames, bool) {
	policyNames := PolicyNames{
		Retry:          r.getDefaultRetryPolicy(policyType),
		Timeout:        r.getDefaultTimeoutPolicy(policyType),
		CircuitBreaker: r.getDefaultCircuitBreakerPolicy(policyType),
	}

	return policyNames, (policyNames.Retry != "" || policyNames.Timeout != "" || policyNames.CircuitBreaker != "")
}

func (r *Resiliency) getDefaultRetryPolicy(policyType PolicyType) string {
	typeTemplates, topLevelTemplate := r.expandPolicyTemplate(policyType, DefaultRetryTemplate)
	for _, typeTemplate := range typeTemplates {
		if _, ok := r.retries[typeTemplate]; ok {
			return typeTemplate
		}
	}

	if _, ok := r.retries[topLevelTemplate]; ok {
		return topLevelTemplate
	}
	return ""
}

func (r *Resiliency) getDefaultTimeoutPolicy(policyType PolicyType) string {
	typeTemplates, topLevelTemplate := r.expandPolicyTemplate(policyType, DefaultTimeoutTemplate)
	for _, typeTemplate := range typeTemplates {
		if _, ok := r.timeouts[typeTemplate]; ok {
			return typeTemplate
		}
	}

	if _, ok := r.timeouts[topLevelTemplate]; ok {
		return topLevelTemplate
	}
	return ""
}

func (r *Resiliency) getDefaultCircuitBreakerPolicy(policyType PolicyType) string {
	typeTemplates, topLevelTemplate := r.expandPolicyTemplate(policyType, DefaultCircuitBreakerTemplate)
	for _, typeTemplate := range typeTemplates {
		if _, ok := r.circuitBreakers[typeTemplate]; ok {
			return typeTemplate
		}
	}

	if _, ok := r.circuitBreakers[topLevelTemplate]; ok {
		return topLevelTemplate
	}
	return ""
}

func (r *Resiliency) expandPolicyTemplate(policyType PolicyType, template DefaultPolicyTemplate) ([]string, string) {
	policyLevels := policyType.getPolicyLevels()
	typeTemplates := make([]string, len(policyLevels))
	for i, level := range policyLevels {
		typeTemplates[i] = fmt.Sprintf(string(template), level)
	}
	return typeTemplates, fmt.Sprintf(string(template), "")
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

// HasRetries returns true if the policy is configured to have more than 1 retry.
func (p PolicyDescription) HasRetries() bool {
	return p.RetryPolicy != nil && p.RetryPolicy.MaxRetries != 0
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

func filterResiliencyConfigs(resiliences []*resiliencyV1alpha.Resiliency, runtimeID string) []*resiliencyV1alpha.Resiliency {
	filteredResiliencies := make([]*resiliencyV1alpha.Resiliency, 0)

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

func (*EndpointPolicy) getPolicyLevels() []string {
	return []string{"App"}
}

func (*EndpointPolicy) getPolicyTypeName() PolicyTypeName {
	return Endpoint
}

func (*ActorPolicy) getPolicyLevels() []string {
	return []string{"Actor"}
}

func (*ActorPolicy) getPolicyTypeName() PolicyTypeName {
	return Actor
}

func (p *ComponentPolicy) getPolicyLevels() []string {
	return []string{
		fmt.Sprintf("%sComponent%s", p.componentType, p.componentDirection),
		fmt.Sprintf("Component%s", p.componentDirection),
		"Component",
	}
}

func (*ComponentPolicy) getPolicyTypeName() PolicyTypeName {
	return Component
}

var ComponentInboundPolicy = ComponentPolicy{
	componentDirection: "Inbound",
}

var ComponentOutboundPolicy = ComponentPolicy{
	componentDirection: "Outbound",
}
