// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

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

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/gorilla/mux"
	"google.golang.org/grpc"
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
	log.Println("performPublish() called")

	var commandBody publishCommand
	err := json.NewDecoder(r.Body).Decode(&commandBody)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	log.Printf("    commandBody.ContentType=%s", commandBody.ContentType)
	log.Printf("    commandBody.Topic=%s", commandBody.Topic)
	log.Printf("    commandBody.Data=%v", commandBody.Data)
	log.Printf("    commandBody.Protocol=%s", commandBody.Protocol)
	log.Printf("    commandBody.Metadata=%s", commandBody.Metadata)

	// based on commandBody.Topic, send to the appropriate topic
	resp := appResponse{Message: fmt.Sprintf("%s is not supported", commandBody.Topic)}

	startTime := epoch()

	jsonValue, err := json.Marshal(commandBody.Data)
	if err != nil {
		log.Printf("Error Marshaling")
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
		status, err = performPublishGRPC(commandBody.Topic, jsonValue, contentType, commandBody.Metadata)
	} else {
		status, err = performPublishHTTP(commandBody.Topic, jsonValue, contentType, commandBody.Metadata)
	}

	if err != nil {
		log.Printf("Publish failed with error=%s, StatusCode=%d", err.Error(), status)

		w.WriteHeader(status)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}

	// pass on status code to the calling application
	w.WriteHeader(status)

	if status == http.StatusOK || status == http.StatusNoContent {
		log.Printf("Publish succeeded")
		resp = appResponse{Message: "Success"}
	} else {
		log.Printf("Publish failed")
		resp = appResponse{Message: "Failed"}
	}
	resp.StartTime = startTime
	resp.EndTime = epoch()

	json.NewEncoder(w).Encode(resp)
}

// nolint:gosec
func performPublishHTTP(topic string, jsonValue []byte, contentType string, metadata map[string]string) (int, error) {
	url := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/%s", daprPortHTTP, pubsubName, topic)
	if len(metadata) > 0 {
		params := net_url.Values{}
		for k, v := range metadata {
			params.Set(fmt.Sprintf("metadata.%s", k), v)
		}
		url = url + "?" + params.Encode()
	}

	log.Printf("Publishing using url %s and body '%s'", url, jsonValue)

	resp, err := http.Post(url, contentType, bytes.NewBuffer(jsonValue))

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

func performPublishGRPC(topic string, jsonValue []byte, contentType string, metadata map[string]string) (int, error) {
	url := fmt.Sprintf("localhost:%d", daprPortGRPC)
	log.Printf("Connecting to dapr using url %s", url)

	req := &runtimev1pb.PublishEventRequest{
		PubsubName:      pubsubName,
		Topic:           topic,
		Data:            jsonValue,
		DataContentType: contentType,
		Metadata:        metadata,
	}
	_, err := grpcClient.PublishEvent(context.Background(), req)

	if err != nil {
		log.Printf("Publish failed: %s", err.Error())

		if strings.Contains(err.Error(), "topic is empty") {
			return http.StatusNotFound, err
		}
		return http.StatusInternalServerError, err
	}
	return http.StatusNoContent, nil
}

func callSubscriberMethod(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	defer r.Body.Close()

	if err != nil {
		log.Printf("Could not read request body: %s", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var req callSubscriberMethodRequest
	json.Unmarshal(body, &req)

	log.Printf("callSubscriberMethod: Call %s on %s", req.Method, req.RemoteApp)

	var resp []byte
	if req.Protocol == "grpc" {
		resp, err = callMethodGRPC(req.RemoteApp, req.Method)
	} else {
		resp, err = callMethodHTTP(req.RemoteApp, req.Method)
	}

	if err != nil {
		log.Printf("Could not get logs from %s: %s", req.RemoteApp, err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Write(resp)
}

func callMethodGRPC(appName, method string) ([]byte, error) {
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

	resp, err := grpcClient.InvokeService(context.Background(), req)
	if err != nil {
		return nil, err
	}

	return resp.Data.Value, nil
}

func callMethodHTTP(appName, method string) ([]byte, error) {
	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s", daprPortHTTP, appName, method)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer([]byte{})) //nolint: gosec

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

	log.Printf("Hello Dapr v2 - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
