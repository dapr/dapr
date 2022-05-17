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
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"google.golang.org/grpc"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

const (
	appPort      = 3000
	daprPortHTTP = 3500
	daprPortGRPC = 50001
	pubsubName   = "messagebus"
)

type publishCommand struct {
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

type callSubscriberMethodRequest struct {
	RemoteApp string `json:"remoteApp"`
	Protocol  string `json:"protocol"`
	Method    string `json:"method"`
}

var (
	grpcConn   *grpc.ClientConn
	grpcClient runtimev1pb.DaprClient
)

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("indexHandler is called\n")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// when called by the test, this function publishes to dapr
// nolint:gosec
func performPublish(w http.ResponseWriter, r *http.Request) {
	reqID := uuid.New().String()

	var commandBody publishCommand
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		log.Printf("(%s) performPublish() called. Failed to parse command body: %v", reqID, err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	{
		enc, _ := json.Marshal(commandBody)
		log.Printf("(%s) performPublish() called with commandBody=%s", reqID, string(enc))
	}

	// based on commandBody.Topic, send to the appropriate topic
	resp := appResponse{Message: fmt.Sprintf("%s is not supported", commandBody.Topic)}

	startTime := epoch()

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

	// publish to dapr
	var status int
	if commandBody.Protocol == "grpc" {
		status, err = performPublishGRPC(reqID, commandBody.Topic, jsonValue, contentType, commandBody.Metadata)
	} else {
		status, err = performPublishHTTP(reqID, commandBody.Topic, jsonValue, contentType, commandBody.Metadata)
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

	if status == http.StatusOK || status == http.StatusNoContent {
		log.Printf("(%s) Publish succeeded", reqID)
		resp = appResponse{Message: "Success"}
	} else {
		log.Printf("(%s) Publish failed", reqID)
		resp = appResponse{Message: "Failed"}
	}
	resp.StartTime = startTime
	resp.EndTime = epoch()

	json.NewEncoder(w).Encode(resp)
}

// nolint:gosec
func performPublishHTTP(reqID string, topic string, jsonValue []byte, contentType string, metadata map[string]string) (int, error) {
	url := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/%s", daprPortHTTP, pubsubName, topic)
	if len(metadata) > 0 {
		params := net_url.Values{}
		for k, v := range metadata {
			params.Set(fmt.Sprintf("metadata.%s", k), v)
		}
		url = url + "?" + params.Encode()
	}

	log.Printf("(%s) Publishing using url %s and body '%s'", reqID, url, jsonValue)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonValue))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if resp != nil {
			return resp.StatusCode, err
		} else {
			return http.StatusInternalServerError, err
		}
	}
	defer resp.Body.Close()

	return resp.StatusCode, nil
}

func performPublishGRPC(reqID string, topic string, jsonValue []byte, contentType string, metadata map[string]string) (int, error) {
	url := fmt.Sprintf("localhost:%d", daprPortGRPC)
	log.Printf("Connecting to dapr using url %s", url)

	req := &runtimev1pb.PublishEventRequest{
		PubsubName:      pubsubName,
		Topic:           topic,
		Data:            jsonValue,
		DataContentType: contentType,
		Metadata:        metadata,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	_, err := grpcClient.PublishEvent(ctx, req)
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

func callSubscriberMethod(w http.ResponseWriter, r *http.Request) {
	reqID := uuid.New().String()

	body, err := io.ReadAll(r.Body)
	defer r.Body.Close()

	if err != nil {
		log.Printf("(%s) Could not read request body: %v", reqID, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var req callSubscriberMethodRequest
	json.Unmarshal(body, &req)

	log.Printf("(%s) callSubscriberMethod: Call %s on %s via %s", reqID, req.Method, req.RemoteApp, req.Protocol)

	var resp []byte
	if req.Protocol == "grpc" {
		resp, err = callSubscriberMethodGRPC(reqID, req.RemoteApp, req.Method)
	} else {
		resp, err = callSubscriberMethodHTTP(reqID, req.RemoteApp, req.Method)
	}

	if err != nil {
		log.Printf("(%s) Could not get logs from %s: %s", reqID, req.RemoteApp, err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(resp)
}

func callSubscriberMethodGRPC(reqID, appName, method string) ([]byte, error) {
	invokeReq := &commonv1pb.InvokeRequest{
		Method: method,
	}
	invokeReq.HttpExtension = &commonv1pb.HTTPExtension{
		Verb: commonv1pb.HTTPExtension_Verb(commonv1pb.HTTPExtension_Verb_value["POST"]),
	}
	req := &runtimev1pb.InvokeServiceRequest{
		Message: invokeReq,
		Id:      appName,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	resp, err := grpcClient.InvokeService(ctx, req)
	cancel()
	if err != nil {
		return nil, err
	}

	return resp.Data.Value, nil
}

func callSubscriberMethodHTTP(reqID, appName, method string) ([]byte, error) {
	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", daprPortHTTP, appName, method)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer([]byte{})) //nolint: gosec
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UTC().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	log.Printf("Enter appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/publish", performPublish).Methods("POST")
	router.HandleFunc("/tests/callSubscriberMethod", callSubscriberMethod).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func initGRPCClient() {
	url := fmt.Sprintf("localhost:%d", daprPortGRPC)
	log.Printf("Connecting to dapr using url %s", url)

	start := time.Now()
	for retries := 10; retries > 0; retries-- {
		var err error
		if grpcConn, err = grpc.Dial(url, grpc.WithInsecure(), grpc.WithBlock()); err == nil {
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

	log.Printf("PubSub Publisher - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
