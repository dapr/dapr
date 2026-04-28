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

package validate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	apiextinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"

	daprcrds "github.com/dapr/dapr/charts/dapr"
	mcpserverapi "github.com/dapr/dapr/pkg/apis/mcpserver/v1alpha1"
)

var (
	celValidator *cel.Validator
	initOnce     sync.Once
	errInit      error
)

func initValidator() {
	// Decode the embedded CRD YAML.
	scheme := runtime.NewScheme()
	if err := apiextensionsv1.AddToScheme(scheme); err != nil {
		errInit = fmt.Errorf("failed to add apiextensions to scheme: %w", err)
		return
	}
	codecs := serializer.NewCodecFactory(scheme)
	obj, _, err := codecs.UniversalDeserializer().Decode(daprcrds.MCPServerCRD, nil, nil)
	if err != nil {
		errInit = fmt.Errorf("failed to decode CRD YAML: %w", err)
		return
	}

	crd, ok := obj.(*apiextensionsv1.CustomResourceDefinition)
	if !ok {
		errInit = fmt.Errorf("decoded object is %T, not CustomResourceDefinition", obj)
		return
	}

	if len(crd.Spec.Versions) == 0 || crd.Spec.Versions[0].Schema == nil ||
		crd.Spec.Versions[0].Schema.OpenAPIV3Schema == nil {
		errInit = errors.New("CRD has no OpenAPI schema")
		return
	}

	v1Schema := crd.Spec.Versions[0].Schema.OpenAPIV3Schema

	// Convert v1 JSONSchemaProps to internal.
	var internalSchema apiextinternal.JSONSchemaProps
	convertErr := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(
		v1Schema, &internalSchema, nil,
	)
	if convertErr != nil {
		errInit = fmt.Errorf("failed to convert schema to internal: %w", convertErr)
		return
	}

	// Build structural schema.
	structural, err := structuralschema.NewStructural(&internalSchema)
	if err != nil {
		errInit = fmt.Errorf("failed to create structural schema: %w", err)
		return
	}

	// Compile CEL validators from the embedded XValidation rules.
	celValidator = cel.NewValidator(structural, true, celconfig.PerCallLimit)
}

// MCPServerSecurity validates security-sensitive fields on an MCPServer.
// Called after CEL validation.
// Rejects unsafe configurations that the schema alone cannot catch.
func MCPServerSecurity(server *mcpserverapi.MCPServer, kubernetesMode bool) error {
	// Stdio transport: reject in Kubernetes mode
	if server.Spec.Endpoint.Stdio != nil {
		if kubernetesMode {
			return fmt.Errorf("MCPServer %q: stdio transport is not allowed in Kubernetes mode", server.Name)
		}
		if server.Spec.Endpoint.Stdio.Command == "" {
			return fmt.Errorf("MCPServer %q: stdio.command must not be empty", server.Name)
		}
		if strings.Contains(server.Spec.Endpoint.Stdio.Command, "..") {
			return fmt.Errorf("MCPServer %q: stdio.command must not contain path traversal", server.Name)
		}
		for _, env := range server.Spec.Endpoint.Stdio.Env {
			if strings.ContainsAny(env.Name, "=\n\x00") {
				return fmt.Errorf("MCPServer %q: stdio.env name %q contains invalid characters", server.Name, env.Name)
			}
		}
	}

	// HTTP URL validation: only allow http:// and https:// schemes.
	if sh := server.Spec.Endpoint.StreamableHTTP; sh != nil {
		if err := validateMCPURL(server.Name, "streamableHTTP", sh.URL); err != nil {
			return err
		}
	}
	if sse := server.Spec.Endpoint.SSE; sse != nil {
		if err := validateMCPURL(server.Name, "sse", sse.URL); err != nil {
			return err
		}
	}

	return nil
}

func validateMCPURL(serverName, transport, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("MCPServer %q: %s.url is not a valid URL: %w", serverName, transport, err)
	}
	switch u.Scheme {
	case "http", "https":
		// ok
	default:
		return fmt.Errorf("MCPServer %q: %s.url scheme must be http or https, got %q", serverName, transport, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("MCPServer %q: %s.url must have a host", serverName, transport)
	}
	return nil
}

// MCPServer validates an MCPServer against the CEL rules and schema
// constraints embedded in the CRD. This provides the same validation in
// standalone mode that the Kubernetes API server provides via CRD admission.
func MCPServer(ctx context.Context, server *mcpserverapi.MCPServer) error {
	initOnce.Do(initValidator)
	if errInit != nil {
		return fmt.Errorf("CEL validator initialization failed: %w", errInit)
	}

	// Convert typed struct to unstructured map for CEL evaluation.
	raw, err := json.Marshal(server)
	if err != nil {
		return fmt.Errorf("failed to marshal MCPServer: %w", err)
	}
	var obj any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("failed to unmarshal MCPServer: %w", err)
	}

	errs, _ := celValidator.Validate(
		ctx,
		field.NewPath(""),
		nil,
		obj,
		nil,
		celconfig.RuntimeCELCostBudget,
	)

	if len(errs) > 0 {
		return errs.ToAggregate()
	}
	return nil
}
