/*
Copyright 2026 The Dapr Authors
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

// operatorscopes calls the Dapr operator API directly with the workload
// identity Sentry issues to this pod's app ID, as any Dapr-enabled pod can, so
// e2e tests can assert what the operator discloses to it.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gorilla/mux"
	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/modes"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/apps/utils"
)

const appPort = 3000

var (
	namespace = os.Getenv("NAMESPACE")
	client    operatorv1pb.OperatorClient
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	trustAnchorsFile := os.Getenv("TRUST_ANCHORS_FILE")
	sec, err := security.New(ctx, security.Options{
		SentryAddress:           os.Getenv("SENTRY_ADDRESS"),
		ControlPlaneTrustDomain: os.Getenv("CONTROL_PLANE_TRUST_DOMAIN"),
		ControlPlaneNamespace:   namespace,
		TrustAnchorsFile:        &trustAnchorsFile,
		AppID:                   os.Getenv("APP_ID"),
		Mode:                    modes.KubernetesMode,
		MTLSEnabled:             true,
		Healthz:                 healthz.New(),
	})
	if err != nil {
		log.Fatalf("failed to create security provider: %v", err)
	}
	go func() {
		if runErr := sec.Run(ctx); runErr != nil {
			log.Fatalf("security provider stopped: %v", runErr)
		}
	}()

	sech, err := sec.Handler(ctx)
	if err != nil {
		log.Fatalf("failed to get security handler: %v", err)
	}
	operatorID, err := spiffeid.FromSegments(sech.ControlPlaneTrustDomain(), "ns", namespace, "dapr-operator")
	if err != nil {
		log.Fatalf("failed to build operator SPIFFE ID: %v", err)
	}
	conn, err := grpc.NewClient(os.Getenv("OPERATOR_ADDRESS"), sech.GRPCDialOptionMTLS(operatorID))
	if err != nil {
		log.Fatalf("failed to dial operator: %v", err)
	}
	defer conn.Close()
	client = operatorv1pb.NewOperatorClient(conn)

	utils.StartServer(appPort, appRouter, true, false)
}

func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)
	router.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	router.HandleFunc("/httpendpoints", func(w http.ResponseWriter, r *http.Request) {
		resp, err := client.ListHTTPEndpoints(r.Context(), &operatorv1pb.ListHTTPEndpointsRequest{Namespace: namespace})
		writeItems(w, resp.GetHttpEndpoints(), err)
	}).Methods(http.MethodGet)
	router.HandleFunc("/mcpservers", func(w http.ResponseWriter, r *http.Request) {
		resp, err := client.ListMCPServers(r.Context(), &operatorv1pb.ListMCPServersRequest{Namespace: namespace})
		writeItems(w, resp.GetMcpServers(), err)
	}).Methods(http.MethodGet)
	router.HandleFunc("/configurations/{name}", func(w http.ResponseWriter, r *http.Request) {
		resp, err := client.GetConfiguration(r.Context(), &operatorv1pb.GetConfigurationRequest{
			Namespace: namespace,
			Name:      mux.Vars(r)["name"],
		})
		var items [][]byte
		if resp != nil {
			items = [][]byte{resp.GetConfiguration()}
		}
		writeItems(w, items, err)
	}).Methods(http.MethodGet)
	return router
}

// writeItems writes the raw resources returned by the operator as a JSON
// array, or maps a PermissionDenied from the operator to 403.
func writeItems(w http.ResponseWriter, items [][]byte, err error) {
	if status.Code(err) == codes.PermissionDenied {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	raw := make([]json.RawMessage, len(items))
	for i, item := range items {
		raw[i] = item
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(raw); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}
