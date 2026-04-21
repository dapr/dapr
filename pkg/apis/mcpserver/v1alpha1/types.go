/*
Copyright 2026 The Dapr Authors
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/apis/common"
	mcpserver "github.com/dapr/dapr/pkg/apis/mcpserver"
)

const (
	Kind    = "MCPServer"
	Version = "v1alpha1"
)

//+genclient
//+genclient:noStatus
//+kubebuilder:object:root=true

// MCPServer describes a Dapr MCPServer resource that declares a connection to
// an MCP (Model Context Protocol) server for durable tool execution.
//
//nolint:recvcheck
type MCPServer struct {
	metav1.TypeMeta `json:",inline"`
	//+optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	//+optional
	Spec MCPServerSpec `json:"spec,omitempty"`
	//+optional
	common.Scoped `json:",inline"`
}

// Kind returns the resource kind.
func (MCPServer) Kind() string {
	return Kind
}

// APIVersion returns the resource API version.
func (MCPServer) APIVersion() string {
	return mcpserver.GroupName + "/" + Version
}

// GetName returns the resource name.
func (m MCPServer) GetName() string {
	return m.Name
}

// GetNamespace returns the resource namespace.
func (m MCPServer) GetNamespace() string {
	return m.Namespace
}

// GetSecretStore returns the name of the secret store used for resolving
// secretKeyRef entries in transport headers during resource processing.
// auth.oauth2.secretKeyRef is resolved separately at call time by the MCP worker.
func (m MCPServer) GetSecretStore() string {
	if auth := m.httpAuth(); auth != nil && auth.SecretStore != nil {
		return *auth.SecretStore
	}
	return ""
}

// LogName returns a human-readable name suitable for log messages.
func (m MCPServer) LogName() string {
	switch {
	case m.Spec.Endpoint.StreamableHTTP != nil:
		return m.Name + " (" + m.Spec.Endpoint.StreamableHTTP.URL + ")"
	case m.Spec.Endpoint.SSE != nil:
		return m.Name + " (" + m.Spec.Endpoint.SSE.URL + ")"
	default:
		return m.Name
	}
}

// NameValuePairs returns the HTTP headers as name/value pairs for secret processing.
func (m MCPServer) NameValuePairs() []common.NameValuePair {
	switch {
	case m.Spec.Endpoint.StreamableHTTP != nil:
		return m.Spec.Endpoint.StreamableHTTP.Headers
	case m.Spec.Endpoint.SSE != nil:
		return m.Spec.Endpoint.SSE.Headers
	default:
		return nil
	}
}

// httpAuth returns the auth config from whichever HTTP transport is configured,
// or nil for stdio.
func (m MCPServer) httpAuth() *MCPAuth {
	switch {
	case m.Spec.Endpoint.StreamableHTTP != nil:
		return m.Spec.Endpoint.StreamableHTTP.Auth
	case m.Spec.Endpoint.SSE != nil:
		return m.Spec.Endpoint.SSE.Auth
	default:
		return nil
	}
}

// GetScopes returns the app scopes for this resource.
func (m MCPServer) GetScopes() []string {
	return m.Scopes
}

// ClientObject returns a controller-runtime client.Object for scheme operations.
func (m MCPServer) ClientObject() client.Object {
	return &m
}

// EmptyMetaDeepCopy returns a new instance with only TypeMeta and Name set.
func (m MCPServer) EmptyMetaDeepCopy() metav1.Object {
	n := m.DeepCopy()
	n.TypeMeta = metav1.TypeMeta{
		Kind:       Kind,
		APIVersion: mcpserver.GroupName + "/" + Version,
	}
	n.ObjectMeta = metav1.ObjectMeta{Name: m.Name}
	return n
}

// MCPServerSpec is the full configuration for an MCP server connection.
type MCPServerSpec struct {
	// Catalog holds user-facing governance metadata (display name, description,
	// owner, tags, links). It is purely informational and not used at runtime.
	//+optional
	Catalog *MCPServerCatalog `json:"catalog,omitempty"`

	// Endpoint describes the transport and target of the MCP server.
	Endpoint MCPEndpoint `json:"endpoint"`

	// Middleware defines optional workflow hooks invoked around each tool call.
	//+optional
	Middleware *MCPMiddleware `json:"middleware,omitempty"`
}

// MCPEndpoint describes how to reach the MCP server.
// Exactly one of StreamableHTTP, SSE, or Stdio must be set.
//
// +kubebuilder:validation:XValidation:rule="(has(self.streamableHTTP) ? 1 : 0) + (has(self.sse) ? 1 : 0) + (has(self.stdio) ? 1 : 0) == 1",message="exactly one of streamableHTTP, sse, or stdio must be set"
type MCPEndpoint struct {
	// StreamableHTTP holds configuration for the streamable_http transport.
	//+optional
	StreamableHTTP *MCPStreamableHTTP `json:"streamableHTTP,omitempty"`

	// SSE holds configuration for the legacy SSE transport.
	//+optional
	SSE *MCPSSE `json:"sse,omitempty"`

	// Stdio holds configuration for the stdio subprocess transport.
	//+optional
	Stdio *MCPStdio `json:"stdio,omitempty"`
}

// MCPStreamableHTTP configures the streamable_http transport.
type MCPStreamableHTTP struct {
	// URL is the endpoint URL of the MCP server.
	//+kubebuilder:validation:MinLength=1
	URL string `json:"url" validate:"required"`

	// ProtocolVersion pins the MCP spec version the server implements,
	// using the date-based format defined by the MCP specification (e.g. "2025-06-18").
	// When unset, the go-sdk negotiates the latest version supported by both sides.
	//+optional
	ProtocolVersion *string `json:"protocolVersion,omitempty"`

	// Timeout is the per-call deadline for MCP requests.
	//+optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Headers are injected on all outbound HTTP requests to the MCP server.
	// Each entry follows the same NameValuePair contract used by HTTPEndpoint:
	// plain value, secretKeyRef, or envRef. Secret resolution uses auth.secretStore.
	//+optional
	Headers []common.NameValuePair `json:"headers,omitempty"`

	// Auth configures authentication for the MCP server connection.
	//+optional
	Auth *MCPAuth `json:"auth,omitempty"`
}

// MCPSSE configures the legacy SSE transport.
type MCPSSE struct {
	// URL is the endpoint URL of the MCP server.
	//+kubebuilder:validation:MinLength=1
	URL string `json:"url" validate:"required"`

	// ProtocolVersion pins the MCP spec version the server implements,
	// using the date-based format defined by the MCP specification (e.g. "2025-06-18").
	// When unset, the go-sdk negotiates the latest version supported by both sides.
	//+optional
	ProtocolVersion *string `json:"protocolVersion,omitempty"`

	// Timeout is the per-call deadline for MCP requests.
	//+optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Headers are injected on all outbound HTTP requests to the MCP server.
	//+optional
	Headers []common.NameValuePair `json:"headers,omitempty"`

	// Auth configures authentication for the MCP server connection.
	//+optional
	Auth *MCPAuth `json:"auth,omitempty"`
}

// MCPStdio configures the stdio subprocess transport.
type MCPStdio struct {
	// Command is the executable to run.
	//+kubebuilder:validation:MinLength=1
	Command string `json:"command" validate:"required"`
	//+optional
	Args []string `json:"args,omitempty"`
	// Env are environment variables injected into the subprocess.
	// Supports plain value, secretKeyRef, and envRef.
	//+optional
	Env []common.NameValuePair `json:"env,omitempty"`
}

// MCPAuth configures authentication for the MCP server connection.
// OAuth2 and SPIFFE are only meaningful when endpoint.target.url is set.
// When target.appID is used (future), Dapr handles mTLS/SPIFFE automatically.
// When target.httpEndpointName is used (future), auth is delegated to that resource.
type MCPAuth struct {
	// SecretStore names the Dapr secret store used to resolve secretKeyRef entries
	// in spec.endpoint.http.headers. auth.oauth2.secretKeyRef is resolved separately
	// at call time by the MCP worker. Defaults to "kubernetes".
	//+optional
	SecretStore *string `json:"secretStore,omitempty"`

	// OAuth2 configures OAuth2 client credentials flow for the MCP server.
	//+optional
	OAuth2 *MCPOAuth2 `json:"oauth2,omitempty"`

	// SPIFFE configures workload identity JWT injection via Dapr's Sentry CA.
	// No secret is required; Sentry issues the SVID automatically.
	//+optional
	SPIFFE *SPIFFE `json:"spiffe,omitempty"`
}

// MCPOAuth2 configures OAuth2 client credentials for authenticating to an MCP server.
type MCPOAuth2 struct {
	// Issuer is the token endpoint of the authorization server.
	//+kubebuilder:validation:MinLength=1
	Issuer string `json:"issuer" validate:"required"`
	//+optional
	Audience *string `json:"audience,omitempty"`
	//+optional
	Scopes []string `json:"scopes,omitempty"`
	// ClientID is the OAuth2 client identifier sent to the token endpoint.
	// Required by RFC 6749 for the standard client_credentials flow (form parameter
	// or HTTP Basic username). May be left empty for non-standard flows (e.g. JWT-bearer
	// assertions or token endpoints that key solely on the client_secret). Client IDs
	// are typically public identifiers; use spec.headers with a secretKeyRef if your
	// environment requires sourcing the value from a secret store.
	//+optional
	ClientID *string `json:"clientID,omitempty"`
	// SecretKeyRef references the client secret in the configured secret store.
	//+optional
	SecretKeyRef *common.SecretKeyRef `json:"secretKeyRef,omitempty"`
}

// SPIFFE configures SPIFFE workload identity for MCP server authentication.
type SPIFFE struct {
	// JWT configures how the SPIFFE SVID JWT is attached to outbound requests.
	//+optional
	JWT *SPIFFEJWT `json:"jwt,omitempty"`
}

// SPIFFEJWT describes how to attach a SPIFFE SVID JWT to outbound MCP requests.
type SPIFFEJWT struct {
	// Header is the HTTP header name to inject the JWT into (e.g. "Authorization").
	//+kubebuilder:validation:MinLength=1
	Header string `json:"header" validate:"required"`
	// HeaderValuePrefix is an optional string prepended to the JWT value (e.g. "Bearer ").
	//+optional
	HeaderValuePrefix *string `json:"headerValuePrefix,omitempty"`
	// Audience is the intended audience for the JWT (e.g. "mcp://payments").
	//+kubebuilder:validation:MinLength=1
	Audience string `json:"audience" validate:"required"`
}

// MCPMiddleware defines optional hook pipelines invoked around tool and list operations.
// Hooks are executed in array order. For "before" hooks, any error aborts the
// chain and the operation. For "after" hooks, errors are logged but do not
// affect the result returned to the caller.
type MCPMiddleware struct {
	// BeforeCallTool hooks are invoked in order before each CallTool.
	// Receives {mcpServer, toolName, arguments} as input.
	// If any hook returns an error, the chain stops and the error is returned
	// as CallToolResult{isError: true}.
	//+optional
	BeforeCallTool []MutatingMCPMiddlewareHook `json:"beforeCallTool,omitempty"`

	// AfterCallTool hooks are invoked in order after each CallTool.
	// Receives {mcpServer, toolName, arguments, result} as input.
	// Errors are logged but do not affect the result.
	//+optional
	AfterCallTool []MutatingMCPMiddlewareHook `json:"afterCallTool,omitempty"`

	// BeforeListTools hooks are invoked in order before each ListTools.
	// Receives {mcpServer} as input.
	// If any hook returns an error, the chain stops and the error is returned.
	//+optional
	BeforeListTools []MCPMiddlewareHook `json:"beforeListTools,omitempty"`

	// AfterListTools hooks are invoked in order after each ListTools.
	// Receives {mcpServer, result} as input.
	// Errors are logged but do not affect the result.
	//+optional
	AfterListTools []MutatingMCPMiddlewareHook `json:"afterListTools,omitempty"`
}

// MCPMiddlewareHook is a single middleware hook. Exactly one field must be set.
// Currently only Workflow is supported; additional hook types (e.g. HTTP callback,
// policy evaluation) may be added in future.
type MCPMiddlewareHook struct {
	// Workflow invokes a Dapr workflow as the hook.
	//+optional
	Workflow *MCPMiddlewareWorkflow `json:"workflow,omitempty"`
}

// MutatingMCPMiddlewareHook extends MCPMiddlewareHook with the ability to
// replace the data flowing through the pipeline when Mutate is true.
type MutatingMCPMiddlewareHook struct {
	MCPMiddlewareHook `json:",inline"`

	// Mutate, when true, causes the hook's return value to replace the data
	// flowing through the pipeline:
	//   - beforeCallTool: replaces the arguments sent to the tool call
	//     (e.g. redact PII, inject defaults).
	//   - afterCallTool / afterListTools: replaces the result returned to the caller.
	// When false (default), the hook validates/observes only — its output is discarded.
	//+optional
	Mutate bool `json:"mutate,omitempty"`
}

// MCPMiddlewareWorkflow identifies a workflow to invoke as a middleware hook.
// When AppID is set, the workflow runs on the remote app via Dapr service invocation.
// When AppID is unset, the workflow runs locally in the same daprd's workflow engine.
type MCPMiddlewareWorkflow struct {
	// WorkflowName is the name of the workflow to invoke.
	//+kubebuilder:validation:MinLength=1
	WorkflowName string `json:"workflowName" validate:"required"`

	// AppID targets the workflow on a remote Dapr app via service invocation.
	// When unset, the workflow is invoked locally.
	//+optional
	AppID *string `json:"appID,omitempty"`
}

// MCPServerCatalog holds user-facing governance metadata. It is purely
// informational and has no effect on runtime behaviour.
type MCPServerCatalog struct {
	//+optional
	DisplayName *string `json:"displayName,omitempty"`
	//+optional
	Description *string `json:"description,omitempty"`
	//+optional
	Owner *MCPCatalogOwner `json:"owner,omitempty"`
	//+optional
	Tags []string `json:"tags,omitempty"`
	// Links is a free-form map of named URLs (e.g. "docs", "runbook", "dashboard").
	//+optional
	Links map[string]string `json:"links,omitempty"`
}

// MCPCatalogOwner identifies the team responsible for the MCP server.
type MCPCatalogOwner struct {
	//+optional
	Team *string `json:"team,omitempty"`
	//+optional
	Contact *string `json:"contact,omitempty"`
}

//+kubebuilder:object:root=true

// MCPServerList is a list of Dapr MCPServer resources.
type MCPServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []MCPServer `json:"items"`
}
