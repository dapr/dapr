// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package kubernetes

import (
	"fmt"

	"github.com/dapr/components-contrib/servicediscovery"
)

type Resolver struct {
}

func NewKubernetesResolver() *Resolver {
	return &Resolver{}
}

func (z *Resolver) ResolveID(req *servicediscovery.ResolveRequest) (string, error) {
	// Dapr requires this formatting for Kubernetes services
	return fmt.Sprintf("%s-dapr.%s.svc.cluster.local:%d", req.ID, req.Namespace, req.Port), nil
}
