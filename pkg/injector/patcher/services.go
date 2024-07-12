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
	"strconv"
	"strings"
)

// Service represents a Dapr control plane service's information.
type Service struct {
	name string
	port int
}

// Predefined services
var (
	// Dapr API service.
	ServiceAPI = Service{"dapr-api", 443}
	// Dapr placement service.
	ServicePlacement = Service{"dapr-placement-server", 50005}
	// Dapr Sentry service.
	ServiceSentry = Service{"dapr-sentry", 443}
)

// NewService returns a Service with values from a string in the format "<name>:<port>"
func NewService(val string) (srv Service, err error) {
	var (
		ok      bool
		portStr string
	)
	srv.name, portStr, ok = strings.Cut(val, ":")
	if !ok || srv.name == "" || portStr == "" {
		return srv, fmt.Errorf("service is not in the correct format '<name>:<port>': %s", val)
	}

	srv.port, err = strconv.Atoi(portStr)
	if err != nil || srv.port <= 0 {
		return srv, fmt.Errorf("service is not in the correct format '<name>:<port>': port is invalid")
	}

	return srv, nil
}

// Address returns the address of a Dapr control plane service
func (svc Service) Address(namespace, clusterDomain string) string {
	return fmt.Sprintf("%s.%s.svc.%s:%d", svc.name, namespace, clusterDomain, svc.port)
}
