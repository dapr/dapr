// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package servicediscovery

const DefaultNamespace = "default"

type ResolveRequest struct {
	ID        string
	Namespace string
	Port      int
	Data      map[string]string
}

func NewResolveRequest() *ResolveRequest {
	return &ResolveRequest{Namespace: DefaultNamespace}
}
