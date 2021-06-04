// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

// ComponentDescription holds dapr component description.
type ComponentDescription struct {
	// Name is the name of dapr component
	Name string
	// Namespace to deploy the component to
	Namespace *string
	// Type contains component types (<type>.<component_name>)
	TypeName string
	// MetaData contains the metadata for dapr component
	MetaData map[string]string
}
