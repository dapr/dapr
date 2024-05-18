/*
Copyright 2021 The Dapr Authors
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

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	chi "github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort = 3000
	/* #nosec */
	metadataURL = "http://localhost:3500/v1.0/metadata"
)

var (
	httpClient *http.Client
	grpcClient runtimev1pb.DaprClient
)

// requestResponse represents a request or response for the APIs in this app.
type requestResponse struct {
	StartTime time.Time `json:"start_time,omitempty"`
	EndTime   time.Time `json:"end_time,omitempty"`
	Message   string    `json:"message,omitempty"`
}

type setMetadataRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type mockMetadata struct {
	ID                   string                    `json:"id"`
	ActiveActorsCount    []activeActorsCount       `json:"actors"`
	Extended             map[string]string         `json:"extended"`
	RegisteredComponents []mockRegisteredComponent `json:"components"`
	EnabledFeatures      []string                  `json:"enabledFeatures"`
}

type activeActorsCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

type mockRegisteredComponent struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

func newMockMetadataFromGrpc(res *runtimev1pb.GetMetadataResponse) mockMetadata {
	activeActors := res.GetActorRuntime().GetActiveActors()
	metadata := mockMetadata{
		ID:                   res.GetId(),
		ActiveActorsCount:    make([]activeActorsCount, len(activeActors)),
		Extended:             res.GetExtendedMetadata(),
		RegisteredComponents: make([]mockRegisteredComponent, len(res.GetRegisteredComponents())),
		EnabledFeatures:      res.GetEnabledFeatures(),
	}

	for i, v := range activeActors {
		metadata.ActiveActorsCount[i] = activeActorsCount{
			Type:  v.GetType(),
			Count: int(v.GetCount()),
		}
	}

	for i, v := range res.GetRegisteredComponents() {
		metadata.RegisteredComponents[i] = mockRegisteredComponent{
			Name:         v.GetName(),
			Type:         v.GetType(),
			Version:      v.GetVersion(),
			Capabilities: v.GetCapabilities(),
		}
	}

	return metadata
}

func indexHandler(w http.ResponseWriter, _ *http.Request) {
	log.Println("indexHandler is called")
	w.WriteHeader(http.StatusOK)
}

func setMetadata(r *http.Request) error {
	var data setMetadataRequest
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		return err
	}

	if data.Key == "" || data.Value == "" {
		return errors.New("key or value in request must be set")
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	switch chi.URLParam(r, "protocol") {
	case "http":
		req, err := http.NewRequestWithContext(ctx, http.MethodPut,
			metadataURL+"/"+data.Key,
			strings.NewReader(data.Value),
		)
		if err != nil {
			return err
		}
		req.Header.Set("content-type", "text/plain")
		res, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusNoContent {
			return fmt.Errorf("invalid status code: %d", res.StatusCode)
		}
	case "grpc":
		_, err := grpcClient.SetMetadata(ctx, &runtimev1pb.SetMetadataRequest{
			Key:   data.Key,
			Value: data.Value,
		})
		if err != nil {
			return fmt.Errorf("failed to set metadata using gRPC: %w", err)
		}
	}

	return nil
}

func getMetadataHTTP(ctx context.Context) (metadata mockMetadata, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return metadata, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return metadata, fmt.Errorf("could not get sidecar metadata %s", err.Error())
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return metadata, fmt.Errorf("could not load value for Metadata: %s", err.Error())
	}
	if res.StatusCode != http.StatusOK {
		log.Printf("Non 200 StatusCode: %d", res.StatusCode)
		return metadata, fmt.Errorf("got err response for get Metadata: %s", body)
	}
	err = json.Unmarshal(body, &metadata)
	return metadata, nil
}

func getMetadataGRPC(ctx context.Context) (metadata mockMetadata, err error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	res, err := grpcClient.GetMetadata(ctx, &runtimev1pb.GetMetadataRequest{})
	if err != nil {
		return metadata, fmt.Errorf("failed to get sidecar metadata from gRPC: %w", err)
	}
	return newMockMetadataFromGrpc(res), nil
}

// Handles tests
func testHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing request for %s", r.URL.RequestURI())

	var (
		metadata mockMetadata
		err      error
		res      requestResponse
	)
	statusCode := http.StatusOK

	res.StartTime = time.Now()

	protocol := chi.URLParam(r, "protocol")
	if protocol != "http" && protocol != "grpc" {
		err = fmt.Errorf("invalid URI: %s", r.URL.RequestURI())
		statusCode = http.StatusBadRequest
		res.Message = err.Error()
	} else {
		switch chi.URLParam(r, "command") {
		case "set":
			err = setMetadata(r)
			if err != nil {
				statusCode = http.StatusInternalServerError
				res.Message = err.Error()
			}
			res.Message = "ok"
		case "get":
			if protocol == "http" {
				metadata, err = getMetadataHTTP(r.Context())
			} else {
				metadata, err = getMetadataGRPC(r.Context())
			}
			if err != nil {
				statusCode = http.StatusInternalServerError
				res.Message = err.Error()
			}
		default:
			err = fmt.Errorf("invalid URI: %s", r.URL.RequestURI())
			statusCode = http.StatusBadRequest
			res.Message = err.Error()
		}
	}

	res.EndTime = time.Now()

	if statusCode != http.StatusOK {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if res.Message == "" {
		json.NewEncoder(w).Encode(metadata)
	} else {
		json.NewEncoder(w).Encode(res)
	}
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := chi.NewRouter()

	router.Use(utils.LoggerMiddleware)

	router.Get("/", indexHandler)
	router.Post("/test/{protocol}/{command}", testHandler)

	return router
}

func main() {
	// Connect to gRPC
	var (
		conn *grpc.ClientConn
		err  error
	)
	grpcSocketAddr := os.Getenv("DAPR_GRPC_SOCKET_ADDR")
	if grpcSocketAddr != "" {
		conn, err = grpc.Dial(
			"unix://"+grpcSocketAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		conn, err = grpc.DialContext(ctx, "localhost:50001",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithBlock(),
		)
	}
	if err != nil {
		log.Fatalf("Failed to establish gRPC connection to Dapr: %v", err)
	}
	grpcClient = runtimev1pb.NewDaprClient(conn)

	// If using Unix Domain Sockets, we need to change the HTTP client too
	httpSocketAddr := os.Getenv("DAPR_HTTP_SOCKET_ADDR")
	if httpSocketAddr != "" {
		httpClient = utils.NewHTTPClientForSocket(httpSocketAddr)
	} else {
		httpClient = utils.NewHTTPClient()
	}

	// Start app
	log.Printf("Metadata App - listening on http://:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
