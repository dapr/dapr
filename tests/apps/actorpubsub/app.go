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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	net_url "net/url"
	"strings"
	"sync"
	"time"

	"github.com/dapr/dapr/pkg/config"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	appPort                 = 3000 // Dapr app port
	daprV1URL               = "http://localhost:3500/v1.0"
	actorMethodURLFormat    = daprV1URL + "/actors/%s/%s/method/%s"
	actorIdleTimeout        = "5s" // Short idle timeout.
	actorScanInterval       = "1s" // Smaller then actorIdleTimeout and short for speedy test.
	drainOngoingCallTimeout = "1s"
	drainRebalancedActors   = true
	daprPortHTTP            = 3500
	daprPortGRPC            = 50001
	defaultpubsubName       = "messagebus"
	defaultTopicNameHTTP    = "myactorpubsubtopic-http"
	defaultTopicNamegRPC    = "myactorpubsubtopic-grpc"
	defaultMethod           = "actormethod"
	defaultActorType        = "actorPubsubTypeE2e"
	firstSubActorType       = "actorTypeTest1Sub" // Actor Type with first subscription registered. Used in NoActoType test
	SecondSubActorType      = "actorTypeTest2Sub" // Actor Type with second subscription registered. Used in NoActoType test
)

type publishCommand struct {
	ReqID       string            `json:"reqID"`
	ContentType string            `json:"contentType"`
	Topic       string            `json:"topic"`
	Data        interface{}       `json:"data"`
	Protocol    string            `json:"protocol"`
	Metadata    map[string]string `json:"metadata"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type daprActor struct {
	actorType string
	id        string
	value     interface{}
}

// represents a response for the APIs in this app.
type actorLogEntry struct {
	Action    string `json:"action,omitempty"`
	ActorType string `json:"actorType,omitempty"`
	ActorID   string `json:"actorId,omitempty"`
	Timestamp int    `json:"timestamp,omitempty"`
}

type daprConfig struct {
	Entities                []string              `json:"entities,omitempty"`
	ActorIdleTimeout        string                `json:"actorIdleTimeout,omitempty"`
	ActorScanInterval       string                `json:"actorScanInterval,omitempty"`
	DrainOngoingCallTimeout string                `json:"drainOngoingCallTimeout,omitempty"`
	DrainRebalancedActors   bool                  `json:"drainRebalancedActors,omitempty"`
	Pubsub                  []config.PubSubConfig `json:"pubsub,omitempty"`
}

// Subscription to topics
var daprConfigResponse = daprConfig{
	Entities:                []string{defaultActorType, firstSubActorType, SecondSubActorType},
	ActorIdleTimeout:        actorIdleTimeout,
	ActorScanInterval:       actorScanInterval,
	DrainOngoingCallTimeout: drainOngoingCallTimeout,
	DrainRebalancedActors:   drainRebalancedActors,
	Pubsub: []config.PubSubConfig{
		{
			PubSubName: defaultpubsubName,
			Topic:      defaultTopicNamegRPC,
			ActorType:  firstSubActorType,
			Method:     defaultMethod,
		},
		{
			PubSubName: defaultpubsubName,
			Topic:      defaultTopicNamegRPC,
			ActorType:  SecondSubActorType,
			Method:     defaultMethod,
		},
		{
			PubSubName: defaultpubsubName,
			Topic:      defaultTopicNameHTTP,
			ActorType:  defaultActorType,
			Method:     defaultMethod,
		},
		{
			PubSubName: defaultpubsubName,
			Topic:      defaultTopicNamegRPC,
			ActorType:  defaultActorType,
			Method:     defaultMethod,
		},
	},
}

var (
	actorLogs      = []actorLogEntry{}
	actorLogsMutex = &sync.Mutex{}
	grpcConn       *grpc.ClientConn
	grpcClient     runtimev1pb.DaprClient
	actors         sync.Map
	httpClient     = utils.NewHTTPClient()
)

// when called by the test, this function publishes to dapr
// nolint:gosec
func performPublish(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	reqID := "s-" + uuid.New().String()

	var commandBody publishCommand
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	r.Body.Close()
	if err != nil {
		log.Printf("(%s) performPublish() called. Failed to parse command body: %v", reqID, err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	if commandBody.ReqID != "" {
		reqID = commandBody.ReqID
	}

	{
		enc, _ := json.Marshal(commandBody)
		log.Printf("(%s) performPublish() called with commandBody=%s", reqID, string(enc))
	}

	// based on commandBody.Topic, send to the appropriate topic
	resp := appResponse{Message: fmt.Sprintf("%s is not supported", commandBody.Topic)}

	jsonValue, err := json.Marshal(commandBody.Data)
	if err != nil {
		log.Printf("(%s) Error Marshaling: %v", reqID, err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	contentType := commandBody.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	actorType := mux.Vars(r)["actorType"]
	if actorType == "NOTACTOR" {
		actorType = ""
	}
	actorID := mux.Vars(r)["actorId"]

	// publish to dapr
	var status int
	if commandBody.Protocol == "grpc" {
		status, err = performPublishGRPC(reqID, commandBody.Topic, jsonValue, contentType, commandBody.Metadata, actorType, actorID)
	} else {
		status, err = performPublishHTTP(reqID, commandBody.Topic, jsonValue, contentType, commandBody.Metadata, actorType, actorID)
	}

	if err != nil {
		log.Printf("(%s) Publish failed with error=%v, StatusCode=%d", reqID, err, status)

		w.WriteHeader(status)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	// pass on status code to the calling application
	w.WriteHeader(status)

	endTime := time.Now()
	duration := endTime.Sub(startTime)
	if status == http.StatusOK || status == http.StatusNoContent {
		log.Printf("(%s) Publish succeeded in %v", reqID, formatDuration(duration))
		resp = appResponse{Message: "Success"}
	} else {
		log.Printf("(%s) Publish failed in %v", reqID, formatDuration(duration))
		resp = appResponse{Message: "Failed"}
	}
	resp.StartTime = epoch()

	json.NewEncoder(w).Encode(resp)
}

// nolint:gosec
func performPublishHTTP(reqID string, topic string, jsonValue []byte, contentType string, metadata map[string]string, actorType string, actorID string) (int, error) {
	url := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/actors/%s/%s/publish/%s/%s", daprPortHTTP, actorType, actorID, defaultpubsubName, topic)
	if len(metadata) > 0 {
		params := net_url.Values{}
		for k, v := range metadata {
			params.Set(fmt.Sprintf("metadata.%s", k), v)
		}
		url = url + "?" + params.Encode()
	}

	log.Printf("(%s) Publishing using url %s and body '%s'. Publish in actor Type: %s and ActorID: %s", reqID, url, jsonValue, actorType, actorID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonValue))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := httpClient.Do(req)
	if err != nil {
		if resp != nil {
			return resp.StatusCode, err
		}
		return http.StatusInternalServerError, err
	}
	resp.Body.Close()

	return resp.StatusCode, nil
}

func performPublishGRPC(reqID string, topic string, jsonValue []byte, contentType string, metadata map[string]string, actorType string, actorID string) (int, error) {
	url := fmt.Sprintf("localhost:%d", daprPortGRPC)
	log.Printf("Connecting to dapr using url %s", url)
	req := &runtimev1pb.PublishActorEventRequest{
		PubsubName:      defaultpubsubName,
		Topic:           topic,
		ActorType:       actorType,
		ActorId:         actorID,
		Data:            jsonValue,
		DataContentType: contentType,
		Metadata:        metadata,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, err := grpcClient.PublishActorEventAlpha1(ctx, req)
	cancel()
	if err != nil {
		log.Printf("(%s) Publish failed: %v", reqID, err)

		if strings.Contains(err.Error(), "topic is empty") {
			return http.StatusNotFound, err
		}
		return http.StatusInternalServerError, err
	}
	return http.StatusNoContent, nil
}

// formatDuration formats the duration in ms
func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Truncate(100*time.Microsecond).Milliseconds())
}

func appendActorLog(logEntry actorLogEntry) {
	actorLogsMutex.Lock()
	defer actorLogsMutex.Unlock()
	actorLogs = append(actorLogs, logEntry)
}

func getActorLogs() []actorLogEntry {
	return actorLogs
}

func createActorID(actorType string, id string) string {
	return fmt.Sprintf("%s.%s", actorType, id)
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

func logsHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing dapr request for %s", r.URL.RequestURI())

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	log.Print(getActorLogs())
	json.NewEncoder(w).Encode(getActorLogs())
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing dapr request for %s", r.URL.RequestURI())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(daprConfigResponse)
}

func actorMethodHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing actor method request for %s", r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	method := mux.Vars(r)["method"]

	actorID := createActorID(actorType, id)
	log.Printf("storing actorID %s\n", actorID)

	actors.Store(actorID, daprActor{
		actorType: actorType,
		id:        actorID,
		value:     nil,
	})
	appendActorLog(actorLogEntry{
		Action:    method,
		ActorType: actorType,
		ActorID:   id,
		Timestamp: epoch(),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

func deactivateActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s actor request for %s", r.Method, r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]

	if actorType != defaultActorType {
		log.Printf("Unknown actor type: %s", actorType)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	actorID := createActorID(actorType, id)
	fmt.Printf("actorID is %s\n", actorID)

	action := ""
	_, ok := actors.Load(actorID)
	log.Printf("loading returned:%t\n", ok)

	if ok && r.Method == "DELETE" {
		action = "deactivation"
		actors.Delete(actorID)
	}

	appendActorLog(actorLogEntry{
		Action:    action,
		ActorType: actorType,
		ActorID:   id,
		Timestamp: epoch(),
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

// calls Dapr's Actor method: simulating actor client call.
// nolint:gosec
func testCallActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s test request for %s", r.Method, r.URL.RequestURI())

	actorType := mux.Vars(r)["actorType"]
	id := mux.Vars(r)["id"]
	method := mux.Vars(r)["method"]

	invokeURL := fmt.Sprintf(actorMethodURLFormat, actorType, id, method)
	log.Printf("Invoking %s", invokeURL)

	res, err := http.Post(invokeURL, "application/json", bytes.NewBuffer([]byte{}))
	if err != nil {
		log.Printf("Could not test actor: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Printf("Could not read actor's test response: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UTC().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/dapr/config", configHandler).Methods("GET")
	router.HandleFunc("/test/logs", logsHandler).Methods("GET")
	router.HandleFunc("/healthz", healthzHandler).Methods("GET")

	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", actorMethodHandler).Methods("PUT")
	router.HandleFunc("/actors/{actorType}/{id}", deactivateActorHandler).Methods("POST", "DELETE")
	router.HandleFunc("/test/{actorType}/{id}/method/{method}", testCallActorHandler).Methods("POST")
	router.HandleFunc("/test/actors/{actorType}/{actorId}/publish", performPublish).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func initGRPCClient() {
	url := fmt.Sprintf("localhost:%d", daprPortGRPC)
	log.Printf("Connecting to dapr using url %s", url)

	start := time.Now()
	for retries := 10; retries > 0; retries-- {
		var err error
		if grpcConn, err = grpc.Dial(url, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock()); err == nil {
			break
		}

		if retries == 0 {
			log.Printf("Could not connect to dapr: %v", err)
			log.Panic(err)
		}

		log.Printf("Could not connect to dapr: %v, retrying...", err)
		time.Sleep(5 * time.Second)
	}
	elapsed := time.Since(start)
	log.Printf("gRPC connect elapsed: %v", elapsed)
	grpcClient = runtimev1pb.NewDaprClient(grpcConn)
}

func main() {
	initGRPCClient()
	log.Printf("Actor App - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true)
}
