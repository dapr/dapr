// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/gorilla/mux"
)

const (
	appPort            = 3000
	daprBaseURL        = "http://localhost:3500/v1.0"
	daprActorMethodURL = daprBaseURL + "/actors/%s/%s/method/%s"

	actorIdleTimeout        = "5s" // Short idle timeout.
	actorScanInterval       = "1s" // Smaller then actorIdleTimeout and short for speedy test.
	drainOngoingCallTimeout = "1s"
	drainRebalancedActors   = true
)

type callRequest struct {
	ActorType       string `json:"actorType"`
	ActorID         string `json:"actorId"`
	Method          string `json:"method"`
	RemoteActorID   string `json:"remoteId,omitempty"`
	RemoteActorType string `json:"remoteType,omitempty"`
}

type daprConfig struct {
	Entities                []string `json:"entities,omitempty"`
	ActorIdleTimeout        string   `json:"actorIdleTimeout,omitempty"`
	ActorScanInterval       string   `json:"actorScanInterval,omitempty"`
	DrainOngoingCallTimeout string   `json:"drainOngoingCallTimeout,omitempty"`
	DrainRebalancedActors   bool     `json:"drainRebalancedActors,omitempty"`
}

var daprConfigResponse = daprConfig{
	[]string{"actor1", "actor2"},
	actorIdleTimeout,
	actorScanInterval,
	drainOngoingCallTimeout,
	drainRebalancedActors,
}

func parseCallRequest(r *http.Request) (callRequest, []byte, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Could not read request body: %s\n", err.Error())
		return callRequest{}, body, err
	}

	var request callRequest
	json.Unmarshal(body, &request)
	return request, body, nil
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// This method is required for actor registration (provides supported types).
func configHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing dapr request for %s", r.URL.RequestURI())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(daprConfigResponse)
}

func callActorMethod(w http.ResponseWriter, r *http.Request) {
	log.Println("callActorMethod is called")

	request, body, err := parseCallRequest(r)
	if err != nil {
		log.Printf("Could not parse request body: %s\n", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	invokeURL := fmt.Sprintf(daprActorMethodURL, request.ActorType, request.ActorID, request.Method)
	log.Printf("Calling actor with: %s\n", invokeURL)

	resp, err := http.Post(invokeURL, "application/json", bytes.NewBuffer(body)) // nolint:gosec
	if resp != nil {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("Resp: %s", string(respBody))
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	}
	if err != nil {
		log.Printf("Failed to call actor: %s\n", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

func logCall(w http.ResponseWriter, r *http.Request) {
	log.Printf("logCall is called")

	actorType := mux.Vars(r)["actorType"]
	actorID := mux.Vars(r)["actorId"]

	resp := fmt.Sprintf("Log call with - actorType: %s, actorId: %s", actorType, actorID)
	log.Println(resp)
	w.Write([]byte(resp))
}

func callDifferentActor(w http.ResponseWriter, r *http.Request) {
	log.Println("callDifferentActor is called")

	request, _, err := parseCallRequest(r)
	if err != nil {
		log.Printf("Could not parse request body: %s\n", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	invokeURL := fmt.Sprintf(daprActorMethodURL, request.RemoteActorType, request.RemoteActorID, "logCall")
	log.Printf("Calling remote actor with: %s\n", invokeURL)

	resp, err := http.Post(invokeURL, "application/json", bytes.NewBuffer([]byte{})) // nolint:gosec
	if resp != nil {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		log.Printf("Resp: %s", string(respBody))
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	}
	if err != nil {
		log.Printf("Failed to call remote actor: %s\n", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
}

// indexHandler is the handler for root path.
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

// appRouter initializes restful api router.
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	// Actor methods are individually bound so we can experiment with missing messages
	router.HandleFunc("/actors/{actorType}/{actorId}/method/logCall", logCall).Methods("POST", "PUT")
	router.HandleFunc("/actors/{actorType}/{actorId}/method/callDifferentActor", callDifferentActor).Methods("POST", "PUT")
	router.HandleFunc("/dapr/config", configHandler).Methods("GET")
	router.HandleFunc("/healthz", healthzHandler).Methods("GET")
	router.HandleFunc("/test/callActorMethod", callActorMethod).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("Actor Invocation App - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
