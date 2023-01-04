/*
Copyright 2022 The Dapr Authors
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

package internal

import (
	"errors"
	"strings"
	"sync"

	daprCredentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/runtime/security"

	"google.golang.org/grpc"
)

var errEstablishingTLSConn = errors.New("failed to establish TLS credentials for actor placement service")

// getGrpcOptsGetter returns a function that provides the grpc options and once defined, a cached version will be returned.
func getGrpcOptsGetter(servers []string, clientCert *daprCredentials.CertChain) func() ([]grpc.DialOption, error) {
	mu := sync.RWMutex{}
	var cached []grpc.DialOption
	return func() ([]grpc.DialOption, error) {
		mu.RLock()
		if cached != nil {
			mu.RUnlock()
			return cached, nil
		}
		mu.RUnlock()
		mu.Lock()
		defer mu.Unlock()

		if cached != nil { // double check lock
			return cached, nil
		}

		opts, err := daprCredentials.GetClientOptions(clientCert, security.TLSServerName)
		if err != nil {
			log.Errorf("%s: %s", errEstablishingTLSConn, err)
			return nil, errEstablishingTLSConn
		}

		if diag.DefaultGRPCMonitoring.IsEnabled() {
			opts = append(
				opts,
				grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()))
		}

		if len(servers) == 1 && strings.HasPrefix(servers[0], "dns:///") {
			// In Kubernetes environment, dapr-placement headless service resolves multiple IP addresses.
			// With round robin load balancer, Dapr can find the leader automatically.
			opts = append(opts, grpc.WithDefaultServiceConfig(grpcServiceConfig))
		}
		cached = opts
		return cached, nil
	}
}
