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
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	grpcRetry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	lru "github.com/hashicorp/golang-lru/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	resiliencyV1alpha "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/resiliency/breaker"
	"github.com/dapr/kit/config"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/retry"
	"github.com/dapr/kit/utils"
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
	Crypto                        ComponentType         = "Crypto"
	Secretstore                   ComponentType         = "Secretstore"
	Statestore                    ComponentType         = "Statestore"
	Inbound                       ComponentDirection    = "Inbound"
	Outbound                      ComponentDirection    = "Outbound"
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
		// PolicyDefined returns true if there's policy that applies to the target.
		PolicyDefined(target string, policyType PolicyType) (exists bool)
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

		actorCBCaches    map[string]*lru.Cache[string, *breaker.CircuitBreaker]
		actorCBsCachesMu sync.RWMutex
		serviceCBs       map[string]*lru.Cache[string, *breaker.CircuitBreaker]
		serviceCBsMu     sync.RWMutex

		componentCBs *circuitBreakerInstances

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

	DefaultPolicyTemplate string
	BuiltInPolicyName     string
	PolicyTypeName        string

	// PolicyTypes have to return an array of their possible levels.
	// Ex. [App], [Actor], [Component, Inbound|Outbound, ComponentType]
	PolicyType interface {
		getPolicyLevels() []string
		getPolicyTypeName() PolicyTypeName
	}
	EndpointPolicy     struct{}
	ActorPolicy        struct{}
	ComponentType      string
	ComponentDirection string
	ComponentPolicy    struct {
		componentType      ComponentType
		componentDirection ComponentDirection
	}
)

// Ensure `*Resiliency` satisfies the `Provider` interface.
var _ = (Provider)((*Resiliency)(nil))

// LoadLocalResiliency loads resiliency configurations from local folders.
func LoadLocalResiliency(log logger.Logger, runtimeID string, paths ...string) []*resiliencyV1alpha.Resiliency {
	configs := []*resiliencyV1alpha.Resiliency{}
	for _, path := range paths {
		loaded := loadLocalResiliencyPath(log, runtimeID, path)
		if len(loaded) > 0 {
			configs = append(configs, loaded...)
		}
	}
	return configs
}

func loadLocalResiliencyPath(log logger.Logger, runtimeID string, path string) []*resiliencyV1alpha.Resiliency {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}

	files, err := os.ReadDir(path)
	if err != nil {
		log.Errorf("Failed to read resiliency files from path %s: %v", path, err)
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
			log.Errorf("Could not read resiliency file %s: %v", file.Name(), err)
			continue
		}

		var ti typeInfo
		if err = yaml.Unmarshal(b, &ti); err != nil {
			log.Errorf("Could not determine resource type: %v", err)
			continue
		}

		if ti.Kind != resiliencyKind {
			continue
		}

		var resiliency resiliencyV1alpha.Resiliency
		if err = yaml.Unmarshal(b, &resiliency); err != nil {
			log.Errorf("Could not parse resiliency file %s: %v", file.Name(), err)
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
		log.Errorf("Error listing resiliency policies: %v", err)
		return nil
	}

	if resp.GetResiliencies() == nil {
		log.Debug("No resiliency policies found")
		return nil
	}

	configs := make([]*resiliencyV1alpha.Resiliency, 0, len(resp.GetResiliencies()))

	for _, b := range resp.GetResiliencies() {
		var resiliency resiliencyV1alpha.Resiliency
		if err = yaml.Unmarshal(b, &resiliency); err != nil {
			log.Errorf("Could not parse resiliency: %v", err)
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
			log.Errorf("Could not read resiliency policy %s: %w", &config.ObjectMeta.Name, err)
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
		actorCBCaches:   make(map[string]*lru.Cache[string, *breaker.CircuitBreaker]),
		serviceCBs:      make(map[string]*lru.Cache[string, *breaker.CircuitBreaker]),
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
		r.serviceCBs[name], err = lru.New[string, *breaker.CircuitBreaker](t.CircuitBreakerCacheSize)
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
		r.actorCBCaches[name], err = lru.New[string, *breaker.CircuitBreaker](t.CircuitBreakerCacheSize)
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

// addMetricsToPolicy adds metrics for resiliency policies for count on instantiation and activation for each policy that is defined.
func (r *Resiliency) addMetricsToPolicy(policyDef *PolicyDefinition, target string, direction diag.PolicyFlowDirection) {
	if policyDef.t != 0 {
		diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.TimeoutPolicy, direction, target)
		policyDef.addTimeoutActivatedMetric = func() {
			diag.DefaultResiliencyMonitoring.PolicyActivated(r.name, r.namespace, diag.TimeoutPolicy, direction, target)
		}
	}
	if policyDef.r != nil {
		diag.DefaultResiliencyMonitoring.PolicyExecuted(r.name, r.namespace, diag.RetryPolicy, direction, target)
		policyDef.addRetryActivatedMetric = func() {
			diag.DefaultResiliencyMonitoring.PolicyActivated(r.name, r.namespace, diag.RetryPolicy, direction, target)
		}
	}
	if policyDef.cb != nil {
		diag.DefaultResiliencyMonitoring.PolicyWithStatusExecuted(r.name, r.namespace, diag.CircuitBreakerPolicy, direction, target, string(policyDef.cb.State()))
		policyDef.addCBStateChangedMetric = func() {
			diag.DefaultResiliencyMonitoring.PolicyWithStatusActivated(r.name, r.namespace, diag.CircuitBreakerPolicy, direction, target, string(policyDef.cb.State()))
		}
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
		}
		if policyNames.Retry != "" {
			policyDef.r = r.retries[policyNames.Retry]
		}
		if policyNames.CircuitBreaker != "" {
			template, ok := r.circuitBreakers[policyNames.CircuitBreaker]
			if ok {
				cache, ok := r.serviceCBs[app]
				if ok {
					policyDef.cb, ok = cache.Get(endpoint)
					if !ok || policyDef.cb == nil {
						policyDef.cb = newCB(endpoint, template, r.log)
						cache.Add(endpoint, policyDef.cb)
					}
				}
			}
		}
	} else {
		if defaultNames, ok := r.getDefaultPolicy(EndpointPolicy{}); ok {
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
					serviceCBCache, err := r.getServiceCBCache(app)
					if err != nil {
						r.log.Errorf("error getting default circuit breaker cache for app %s: %s", app, err)
					}
					policyDef.cb = r.getCBFromCache(serviceCBCache, endpoint, template)
				}
			}
		}
	}
	r.addMetricsToPolicy(policyDef, diag.ResiliencyAppTarget(app), diag.OutboundPolicyFlowDirection)

	return policyDef
}

func newCB(cbName string, template *breaker.CircuitBreaker, l logger.Logger) *breaker.CircuitBreaker {
	cb := &breaker.CircuitBreaker{
		Name:        cbName,
		MaxRequests: template.MaxRequests,
		Interval:    template.Interval,
		Timeout:     template.Timeout,
		Trip:        template.Trip,
	}
	cb.Initialize(l)
	return cb
}

func (r *Resiliency) getServiceCBCache(app string) (*lru.Cache[string, *breaker.CircuitBreaker], error) {
	r.serviceCBsMu.RLock()
	cache, ok := r.serviceCBs[app]
	r.serviceCBsMu.RUnlock()

	if ok {
		return cache, nil
	}

	r.serviceCBsMu.Lock()
	defer r.serviceCBsMu.Unlock()

	cache, ok = r.serviceCBs[app] // double check in case another goroutine created the cache
	if ok {
		return cache, nil
	}

	cache, err := lru.New[string, *breaker.CircuitBreaker](defaultEndpointCacheSize)
	if err != nil {
		return nil, err
	}
	r.serviceCBs[app] = cache
	return cache, nil
}

func (r *Resiliency) getActorCBCache(app string) (*lru.Cache[string, *breaker.CircuitBreaker], error) {
	r.actorCBsCachesMu.RLock()
	cache, ok := r.actorCBCaches[app]
	r.actorCBsCachesMu.RUnlock()

	if ok {
		return cache, nil
	}

	r.actorCBsCachesMu.Lock()
	defer r.actorCBsCachesMu.Unlock()

	cache, ok = r.actorCBCaches[app] // double check in case another goroutine created the cache
	if ok {
		return cache, nil
	}

	cache, err := lru.New[string, *breaker.CircuitBreaker](defaultEndpointCacheSize)
	if err != nil {
		return nil, err
	}
	r.actorCBCaches[app] = cache
	return cache, nil
}

func (r *Resiliency) getCBFromCache(cache *lru.Cache[string, *breaker.CircuitBreaker], key string, template *breaker.CircuitBreaker) *breaker.CircuitBreaker {
	if cache == nil {
		return newCB(key, template, r.log)
	}
	cb, ok := cache.Get(key)
	if !ok || cb == nil {
		r.serviceCBsMu.Lock()
		defer r.serviceCBsMu.Unlock()

		cb, ok = cache.Get(key)
		if ok && cb != nil {
			return cb
		}

		cb = newCB(key, template, r.log)
		cache.Add(key, cb)
	}
	return cb
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

					policyDef.cb, ok = cache.Get(key)
					if !ok || policyDef.cb == nil {
						policyDef.cb = newCB(key, template, r.log)
						cache.Add(key, policyDef.cb)
					}
				}
			}
		}
	} else {
		if defaultNames, ok := r.getDefaultPolicy(ActorPolicy{}); ok {
			r.log.Debugf("Found Default Policy for Actor type %s: %+v", actorType, defaultNames)
			if defaultNames.Retry != "" {
				policyDef.r = r.retries[defaultNames.Retry]
			}
			if defaultNames.CircuitBreaker != "" {
				template, ok := r.circuitBreakers[defaultNames.CircuitBreaker]
				if ok {
					actorCBCache, err := r.getActorCBCache(actorType)
					if err != nil {
						r.log.Errorf("error getting default circuit breaker cache for actor type %s: %v", actorType, err)
					}
					policyDef.cb = r.getCBFromCache(actorCBCache, actorType, template)
				}
			}
		}
	}
	r.addMetricsToPolicy(policyDef, diag.ResiliencyActorTarget(actorType), diag.OutboundPolicyFlowDirection)

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
		}
	} else {
		if defaultPolicies, ok := r.getDefaultPolicy(ActorPolicy{}); ok {
			r.log.Debugf("Found Default Policy for Actor type %s: %+v", actorType, defaultPolicies)
			if defaultPolicies.Timeout != "" {
				policyDef.t = r.timeouts[defaultPolicies.Timeout]
			}
		}
	}
	r.addMetricsToPolicy(policyDef, diag.ResiliencyActorTarget(actorType), diag.OutboundPolicyFlowDirection)

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
		}
		if componentPolicies.Outbound.Retry != "" {
			policyDef.r = r.retries[componentPolicies.Outbound.Retry]
		}
		if componentPolicies.Outbound.CircuitBreaker != "" {
			template := r.circuitBreakers[componentPolicies.Outbound.CircuitBreaker]
			policyDef.cb = r.componentCBs.Get(r.log, name, template)
		}
	} else {
		if defaultPolicies, ok := r.getDefaultPolicy(ComponentPolicy{componentType: componentType, componentDirection: "Outbound"}); ok {
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
	r.addMetricsToPolicy(policyDef, diag.ResiliencyComponentTarget(name, string(componentType)), diag.OutboundPolicyFlowDirection)

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
		}
		if componentPolicies.Inbound.Retry != "" {
			policyDef.r = r.retries[componentPolicies.Inbound.Retry]
		}
		if componentPolicies.Inbound.CircuitBreaker != "" {
			template := r.circuitBreakers[componentPolicies.Inbound.CircuitBreaker]
			policyDef.cb = r.componentCBs.Get(r.log, name, template)
		}
	} else {
		if defaultPolicies, ok := r.getDefaultPolicy(ComponentPolicy{componentType: componentType, componentDirection: Inbound}); ok {
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
	r.addMetricsToPolicy(policyDef, diag.ResiliencyComponentTarget(name, string(componentType)), diag.InboundPolicyFlowDirection)

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

// PolicyDefined returns true if there's policy that applies to the target.
func (r *Resiliency) PolicyDefined(target string, policyType PolicyType) (exists bool) {
	switch policyType.getPolicyTypeName() {
	case Endpoint:
		_, exists = r.apps[target]
	case Component:
		_, exists = r.components[target]
	case Actor:
		_, exists = r.actors[target]
	}
	return exists
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

	// Must create a new object
	e.Lock()
	defer e.Unlock()

	// Check again in case another goroutine created the object while we were waiting for the lock
	cb, ok = e.cbs[instanceName]
	if ok {
		return cb
	}

	cb = newCB(template.Name+"-"+instanceName, template, log)

	e.cbs[instanceName] = cb

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

// IsTimeExceeded returns true if the context timeout has elapsed.
func IsTimeoutExeceeded(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

// IsCircuitBreakerError returns true if the error is cicuit breaker open or too many requests in half-open state.
func IsCircuitBreakerError(err error) bool {
	return errors.Is(err, breaker.ErrOpenState) || errors.Is(err, breaker.ErrTooManyRequests)
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

func (EndpointPolicy) getPolicyLevels() []string {
	return []string{"App"}
}

func (EndpointPolicy) getPolicyTypeName() PolicyTypeName {
	return Endpoint
}

func (ActorPolicy) getPolicyLevels() []string {
	return []string{"Actor"}
}

func (ActorPolicy) getPolicyTypeName() PolicyTypeName {
	return Actor
}

func (p ComponentPolicy) getPolicyLevels() []string {
	return []string{
		string(p.componentType) + "Component" + string(p.componentDirection),
		"Component" + string(p.componentDirection),
		"Component",
	}
}

func (ComponentPolicy) getPolicyTypeName() PolicyTypeName {
	return Component
}

var ComponentInboundPolicy = ComponentPolicy{
	componentDirection: Inbound,
}

var ComponentOutboundPolicy = ComponentPolicy{
	componentDirection: Outbound,
}
