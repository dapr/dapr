// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package servicediscovery

import (
	"github.com/dapr/components-contrib/servicediscovery/kubernetes"
	"github.com/dapr/components-contrib/servicediscovery/mdns"
)

func Load() {
	RegisterServiceDiscovery("mdns", mdns.NewMDNSResolver)

	RegisterServiceDiscovery("kubernetes", kubernetes.NewKubernetesResolver)
}
