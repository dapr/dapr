// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

// ComponentDescription holds dapr component description
type ComponentDescription struct {
	// Name is the name of dapr component
	Name string
	// Type contains component types (<type>.<component_name>)
	TypeName string
	// MetaData contains the metadata for dapr component
	MetaData map[string]string
}
