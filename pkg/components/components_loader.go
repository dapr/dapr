// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"

// ComponentLoader is an interface for returning Dapr components.
type ComponentLoader interface {
	LoadComponents() ([]components_v1alpha1.Component, error)
}
