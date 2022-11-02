package semconv

import "go.opentelemetry.io/otel/attribute"

// Component
const (
	ComponentProcessStatusKey = attribute.Key("dapr.component.pubsub.process_status")
	ComponentSuccessKey       = attribute.Key("dapr.component.success")
	ComponentTopicKey         = attribute.Key("dapr.component.pubsub.topic")
	ComponentNameKey          = attribute.Key("dapr.component.name")
	ComponentTypeKey          = attribute.Key("dapr.component.type")
	ComponentOperationKey     = attribute.Key("dapr.component.operation")
	ComponentMethodKey        = attribute.Key("dapr.component.method")
)

var (
	// state
	ComponentStates   = ComponentTypeKey.String("state")
	ComponentSecrets  = ComponentTypeKey.String("secrets")
	ComponentBindings = ComponentTypeKey.String("bindings")
	ComponentPubsub   = ComponentTypeKey.String("pubsub")
)

// Resiliency
const (
	ResiliencyNameKey   = attribute.Key("resiliency.name")
	ResiliencyPolicyKey = attribute.Key("resiliency.policy")
)

var (
	// resiliency policy.
	ResiliencyCircuitBreakerPolicy = ResiliencyPolicyKey.String("circuitbreaker")
	ResiliencyRetryPolicy          = ResiliencyPolicyKey.String("retry")
	ResiliencyTimeoutPolicy        = ResiliencyPolicyKey.String("timeout")
)

// RPC
const (
	RPCTypeKey   = attribute.Key("rpc.type")
	RPCStatusKey = attribute.Key("rpc.status")
)

var (
	// Server
	RPCTypeServer = RPCTypeKey.String("server")
	// Client
	RPCTypeClient = RPCTypeKey.String("client")
)

// Dapr global
const (
	FailReasonKey      = attribute.Key("dapr.reason")
	TrustDomainKey     = attribute.Key("dapr.trust_domain")
	PolicyActionKey    = attribute.Key("dapr.policy_action")
	APIInvokeMethodKey = attribute.Key("dapr.invoke_method")
	APIKey             = attribute.Key("dapr.api")
	APIStatusCodeKey   = attribute.Key("dapr.status_code")
	APIProtocolKey     = attribute.Key("dapr.protocol")
)

var (
	// grpc
	APIProtocolGRPC = APIProtocolKey.String("grpc")
	// http
	APIProtocolHTTP = APIProtocolKey.String("http")
)

// Actor
const (
	APIActorTypeIDKey = attribute.Key("dapr.actor.id")
	APIActorTypeKey   = attribute.Key("dapr.actor.type")
)
