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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	runtimev1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	daprBaseURLFormat               = "http://localhost:%d/v1.0"
	actorInvokeURLFormat            = daprBaseURLFormat + "/actors/%s/%s/method/test"
	actorSaveStateURLFormat         = daprBaseURLFormat + "/actors/%s/%s/state"
	actorGetStateURLFormat          = daprBaseURLFormat + "/actors/%s/%s/state/%s"
	actorTypeEnvName                = "TEST_APP_ACTOR_TYPE"                 // To set to change actor type.
	actorRemindersPartitionsEnvName = "TEST_APP_ACTOR_REMINDERS_PARTITIONS" // To set actor type partition count.
)

var (
	appPort      = 3000
	daprHTTPPort = 3500
	daprGRPCPort = 50001
	httpClient   = utils.NewHTTPClient()
)

func init() {
	p := os.Getenv("DAPR_HTTP_PORT")
	if p != "" && p != "0" {
		daprHTTPPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("DAPR_GRPC_PORT")
	if p != "" && p != "0" {
		daprGRPCPort, _ = strconv.Atoi(p)
	}
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

func actorStateHandlerGRPC(w http.ResponseWriter, r *http.Request) {
	daprAddress := fmt.Sprintf("localhost:%d", daprGRPCPort)
	conn, err := grpc.DialContext(r.Context(), daprAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Printf("gRPC dapr connection failed %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer conn.Close()

	client := runtimev1.NewDaprClient(conn)
	switch r.Method {
	case http.MethodGet:
		var req runtimev1.GetActorStateRequest
		if err = json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("actor state call failed: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		var resp *runtimev1.GetActorStateResponse
		resp, err = client.GetActorState(r.Context(), &req)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
		} else {
			b, _ := json.Marshal(resp)
			w.Write(b)
			w.WriteHeader(http.StatusOK)
		}

	default:
		var op runtimev1.ExecuteActorStateTransactionRequest
		if err = json.NewDecoder(r.Body).Decode(&op); err != nil {
			log.Printf("actor state call failed: %s", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		_, err := client.ExecuteActorStateTransaction(r.Context(), &op)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(err.Error()))
		} else {
			w.WriteHeader(http.StatusOK)
		}
	}
}

func actorStateHandlerHTTP(w http.ResponseWriter, r *http.Request) {
	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]

	var url string
	switch r.Method {
	case http.MethodGet, http.MethodDelete:
		url = fmt.Sprintf(actorGetStateURLFormat, daprHTTPPort, actorType, id, mux.Vars(r)["key"])
	default:
		url = fmt.Sprintf(actorSaveStateURLFormat, daprHTTPPort, actorType, id)
	}

	resp, status, header, err := httpCall(r.Method, url, r.Body)
	if err != nil {
		log.Printf("actor state call failed: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	for k, v := range header {
		w.Header().Set(k, v[0])
	}
	w.WriteHeader(status)
	w.Write(resp)
}

func initActor(w http.ResponseWriter, r *http.Request) {
	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]

	reqURL := fmt.Sprintf(actorInvokeURLFormat, daprHTTPPort, actorType, id)
	req, err := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(nil))
	if err != nil {
		log.Printf("actor init call failed: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("actor init call failed: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("actor init call failed: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write(b)
}

func actorMethodHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing dapr request for %s", r.URL.RequestURI())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(struct {
		Entities                []string `json:"entities,omitempty"`
		ActorIdleTimeout        string   `json:"actorIdleTimeout,omitempty"`
		ActorScanInterval       string   `json:"actorScanInterval,omitempty"`
		DrainOngoingCallTimeout string   `json:"drainOngoingCallTimeout,omitempty"`
		DrainRebalancedActors   bool     `json:"drainRebalancedActors,omitempty"`
	}{
		Entities:                []string{"httpMyActorType", "grpcMyActorType"},
		ActorIdleTimeout:        "30s",
		ActorScanInterval:       "10s",
		DrainOngoingCallTimeout: "20s",
		DrainRebalancedActors:   true,
	})
}

func httpCall(method string, url string, body io.ReadCloser) ([]byte, int, http.Header, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, -1, nil, err
	}

	req.Header.Set("Content-Type", "application/json; utf-8")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, -1, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, -1, nil, err
	}

	return respBody, resp.StatusCode, resp.Header, nil
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/dapr/config", configHandler).Methods("GET")
	router.HandleFunc("/healthz", healthzHandler).Methods("GET")
	router.HandleFunc("/test/initactor/{actorType}/{id}", initActor).Methods("GET")
	router.HandleFunc("/test/actor_state_http/{actorType}/{id}/{key}", actorStateHandlerHTTP).Methods("GET", "DELETE")
	router.HandleFunc("/test/actor_state_http/{actorType}/{id}", actorStateHandlerHTTP).Methods("PUT", "POST", "PATCH")
	router.HandleFunc("/test/actor_state_grpc", actorStateHandlerGRPC).Methods("GET", "POST", "DELETE", "PATCH")
	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", actorMethodHandler).Methods("PUT", "POST")
	router.HandleFunc("/actors/{actorType}/{id}", actorMethodHandler).Methods("POST", "DELETE")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Actor App - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
