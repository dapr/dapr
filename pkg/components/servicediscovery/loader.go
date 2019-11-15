// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package servicediscovery

import (
	"github.com/dapr/components-contrib/servicediscovery"
	"github.com/dapr/components-contrib/servicediscovery/kubernetes"
	"github.com/dapr/components-contrib/servicediscovery/mdns"
)

func Load() {
	RegisterServiceDiscovery("mdns", func() servicediscovery.Resolver {
		return mdns.NewMDNSResolver()
	})
	RegisterServiceDiscovery("kubernetes", func() servicediscovery.Resolver {
		return kubernetes.NewKubernetesResolver()
	})
}
