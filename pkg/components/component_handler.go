// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package components

import components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"

// ComponentHandler is an interface for reacting on component changes.
type ComponentHandler interface {
	OnComponentUpdated(component components_v1alpha1.Component)
}
