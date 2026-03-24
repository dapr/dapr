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

const kind = "MCPServer"

// Kind returns the resource kind.
func (MCPServer) Kind() string {
	return kind
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
// secretKeyRef entries in spec.headers and auth.oauth2 credentials.
func (m MCPServer) GetSecretStore() string {
	if m.Spec.Auth == nil || m.Spec.Auth.SecretStore == nil {
		return ""
	}
	return *m.Spec.Auth.SecretStore
}

// LogName returns a human-readable name suitable for log messages.
func (m MCPServer) LogName() string {
	if m.Spec.Endpoint.Target.URL != "" {
		return m.Name + " (" + m.Spec.Endpoint.Target.URL + ")"
	}
	return m.Name
}

// NameValuePairs returns the headers as name/value pairs for secret processing.
func (m MCPServer) NameValuePairs() []common.NameValuePair {
	return m.Spec.Headers
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

	// Headers are injected on all outbound HTTP transport requests to the MCP server:
	// once per SSE connection handshake, or per-request for streamable_http.
	// Not applicable when endpoint.transport is "stdio".
	// Each entry follows the same NameValuePair contract used by HTTPEndpoint:
	// plain value, secretKeyRef, or envRef. Secret resolution uses auth.secretStore.
	//+optional
	Headers []common.NameValuePair `json:"headers,omitempty"`

	// Auth configures authentication for the MCP server connection.
	//+optional
	Auth *MCPAuth `json:"auth,omitempty"`

	// Middleware defines optional workflow hooks invoked around each tool call.
	//+optional
	Middleware *MCPMiddleware `json:"middleware,omitempty"`

	// Stdio is only valid when endpoint.transport is "stdio".
	//+optional
	Stdio *MCPStdioSpec `json:"stdio,omitempty"`
}

// MCPTransport identifies the wire transport used to reach the MCP server.
type MCPTransport string

const (
	MCPTransportStreamableHTTP MCPTransport = "streamable_http"
	MCPTransportSSE            MCPTransport = "sse"
	MCPTransportStdio          MCPTransport = "stdio"
)

// MCPEndpoint describes the transport and target of the MCP server.
type MCPEndpoint struct {
	// Transport is the wire transport: streamable_http, sse, or stdio.
	Transport MCPTransport `json:"transport"`

	// Target is a one-of identifying the MCP server.
	// Only url is supported in v1. Future iterations add appID and httpEndpointName.
	Target MCPEndpointTarget `json:"target"`

	// ProtocolVersion pins the MCP spec version the server implements.
	// Used by the go-sdk client to negotiate correctly.
	//+optional
	ProtocolVersion *string `json:"protocolVersion,omitempty"`

	// Timeout is the per-call deadline for MCP requests.
	//+optional
	Timeout *metav1.Duration `json:"timeout,omitempty"`
}

// MCPEndpointTarget is a one-of: set exactly one field.
// In v1 only URL is supported. AppID and HTTPEndpointName are reserved for
// future iterations.
type MCPEndpointTarget struct {
	// URL is the raw endpoint URL of the MCP server.
	//+optional
	URL string `json:"url,omitempty"`
	// AppID and HTTPEndpointName are reserved for future iterations.
}

// MCPStdioSpec configures a local stdio MCP server subprocess.
// Only valid when endpoint.transport is "stdio". Not supported in Kubernetes mode.
type MCPStdioSpec struct {
	// Command is the executable to run.
	Command string `json:"command"`
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
	// in spec.headers and OAuth2 credentials. Defaults to "kubernetes".
	//+optional
	SecretStore *string `json:"secretStore,omitempty"`

	// OAuth2 configures OAuth2 client credentials flow for the MCP server.
	//+optional
	OAuth2 *MCPOAuth2 `json:"oauth2,omitempty"`

	// SPIFFE configures workload identity JWT injection via Dapr's Sentry CA.
	// No secret is required; Sentry issues the SVID automatically.
	//+optional
	SPIFFE *SPIFFESpec `json:"spiffe,omitempty"`
}

// MCPOAuth2 configures OAuth2 client credentials for authenticating to an MCP server.
type MCPOAuth2 struct {
	// Issuer is the token endpoint of the authorization server.
	Issuer string `json:"issuer"`
	//+optional
	Audience *string `json:"audience,omitempty"`
	//+optional
	Scopes []string `json:"scopes,omitempty"`
	// SecretKeyRef references the client secret in the configured secret store.
	//+optional
	SecretKeyRef *common.SecretKeyRef `json:"secretKeyRef,omitempty"`
}

// SPIFFESpec configures SPIFFE workload identity for MCP server authentication.
type SPIFFESpec struct {
	// JWT configures how the SPIFFE SVID JWT is attached to outbound requests.
	//+optional
	JWT *SPIFFEJWTSpec `json:"jwt,omitempty"`
}

// SPIFFEJWTSpec describes how to attach a SPIFFE SVID JWT to outbound MCP requests.
type SPIFFEJWTSpec struct {
	// Header is the HTTP header name to inject the JWT into (e.g. "Authorization").
	Header string `json:"header" validate:"required"`
	// Audience is the intended audience for the JWT (e.g. "mcp://payments").
	Audience string `json:"audience" validate:"required"`
	// Prefix is an optional string prepended to the JWT value (e.g. "Bearer ").
	//+optional
	Prefix *string `json:"prefix,omitempty"`
}

// MCPMiddleware defines optional workflow hooks invoked around each tool call.
type MCPMiddleware struct {
	// BeforeCall names a user-registered workflow invoked before each ListTools/CallTool.
	// Receives {mcpServer, tool, arguments} as input.
	// If this workflow returns an error the tool call is aborted and the error is
	// returned as CallToolResult{isError: true} to the caller.
	//+optional
	BeforeCall *string `json:"beforeCall,omitempty"`

	// AfterCall names a user-registered workflow invoked after each ListTools/CallTool.
	// Receives {mcpServer, tool, arguments, result} as input.
	// Errors from this workflow are logged but do not affect the result returned to
	// the caller — the MCP call has already completed.
	//+optional
	AfterCall *string `json:"afterCall,omitempty"`
}

// MCPServerCatalog holds user-facing governance metadata. It is purely
// informational and has no effect on runtime behaviour.
type MCPServerCatalog struct {
	//+optional
	DisplayName string `json:"displayName,omitempty"`
	//+optional
	Description string `json:"description,omitempty"`
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
	Team string `json:"team,omitempty"`
	//+optional
	Contact string `json:"contact,omitempty"`
}

//+kubebuilder:object:root=true

// MCPServerList is a list of Dapr MCPServer resources.
type MCPServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []MCPServer `json:"items"`
}
