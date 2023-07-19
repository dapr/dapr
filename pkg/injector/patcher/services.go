/*
Copyright 2023 The Dapr Authors
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

package patcher

import (
	"fmt"
)

// Service represents a Dapr control plane service's information.
type Service struct {
	name string
	port int
}

var (
	// Dapr API service.
	ServiceAPI = Service{"dapr-api", 80}
	// Dapr placement service.
	ServicePlacement = Service{"dapr-placement-server", 50005}
	// Dapr sentry service.
	ServiceSentry = Service{"dapr-sentry", 80}
)

// ServiceAddress returns the address of a Dapr control plane service.
func ServiceAddress(svc Service, namespace string, clusterDomain string) string {
	return fmt.Sprintf("%s.%s.svc.%s:%d", svc.name, namespace, clusterDomain, svc.port)
}
