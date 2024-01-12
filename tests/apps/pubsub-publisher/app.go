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
	netUrl "net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	daprhttp "github.com/dapr/dapr/pkg/api/http"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort      = 3000
	daprPortHTTP = 3500
	daprPortGRPC = 50001

	metadataPrefix       = "metadata."
	PubSubEnvVar         = "DAPR_TEST_PUBSUB_NAME"
	bulkPubsubMetaKey    = "bulkPublishPubsubName"
	bulkSubTopicIdentity = "sub-topic"
)

type bulkPublishMessageEntry struct {
	EntryId     string            `json:"entryId,omitempty"` //nolint:stylecheck
	Event       interface{}       `json:"event"`
	ContentType string            `json:"ContentType"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

var (
	pubsubName  = "messagebus"
	pubsubKafka = "kafka-messagebus"
)

func init() {
	if psName := os.Getenv(PubSubEnvVar); len(psName) != 0 {
		pubsubName = psName
	}
}

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

type callSubscriberMethodRequest struct {
	ReqID     string `json:"reqID"`
	RemoteApp string `json:"remoteApp"`
	Protocol  string `json:"protocol"`
	Method    string `json:"method"`
}

var (
	grpcClient runtimev1pb.DaprClient
	httpClient = utils.NewHTTPClient()
)

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("indexHandler is called\n")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

func getBulkRequestMetadata(r *http.Request) map[string]string {
	metadata := map[string]string{}
	for k, v := range r.URL.Query() {
		if strings.HasPrefix(k, metadataPrefix) {
			m := strings.TrimPrefix(k, metadataPrefix)
			metadata[m] = v[len(v)-1] // get only last occurring value?
			log.Printf("found metadata %s = %s\n", m, v[len(v)-1])
		}
	}
	return metadata
}

func getBulkPublishPubsubOrDefault(reqMeta map[string]string) string {
	if v, ok := reqMeta[bulkPubsubMetaKey]; ok {
		delete(reqMeta, bulkPubsubMetaKey)
		return v
	}
	return pubsubName
}

// when called by the test, this function publishes to dapr
func performBulkPublish(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()
	reqID := "s-" + uuid.New().String()

	reqMetadata := getBulkRequestMetadata(r)
	pubsubToPublish := getBulkPublishPubsubOrDefault(reqMetadata)
	log.Printf("publishing to pubsub %s", pubsubToPublish)
	var commands []publishCommand
	err := json.NewDecoder(r.Body).Decode(&commands)
	r.Body.Close()
	if err != nil {
		log.Printf("(%s) performBulkPublish() called. Failed to parse command body: %v", reqID, err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}
	topic := ""
	protocol := ""
	if len(commands) > 0 {
		topic = commands[0].Topic
		protocol = commands[0].Protocol
	} else {
		log.Printf("(%s) performBulkPublish() called. No commands to execute", reqID)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: fmt.Sprintf("(%s) performBulkPublish() called. No commands to execute", reqID),
		})
		return
	}
	bulkPublishMessage := make([]bulkPublishMessageEntry, len(commands))

	for i, command := range commands {
		if command.Topic != topic {
			msg := fmt.Sprintf("(%s) performBulkPublish() called. Different topics given for different commands %s, %s", reqID, topic, command.Topic)
			log.Print(msg)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(appResponse{
				Message: msg,
			})
			return
		}
		if command.Protocol != protocol {
			msg := fmt.Sprintf("(%s) performBulkPublish() called. Different protocols given for different commands %s, %s", reqID, protocol, command.Protocol)
			log.Print(msg)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(appResponse{
				Message: msg,
			})
			return
		}
		bulkPublishMessage[i].EntryId = strconv.Itoa(i)

		if command.ReqID != "" {
			reqID = command.ReqID
		}

		enc, _ := json.Marshal(command)
		log.Printf("(%s) performBulkPublish() called with publishCommand=%s", reqID, string(enc))

		bulkPublishMessage[i].Event = command.Data

		contentType := command.ContentType
		if contentType == "" {
			msg := fmt.Sprintf("(%s) performBulkPublish() called. Missing contentType for some events", reqID)
			log.Print(msg)
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(appResponse{
				Message: msg,
			})
			return
		}

		bulkPublishMessage[i].ContentType = contentType

		if command.Metadata != nil {
			bulkPublishMessage[i].Metadata = map[string]string{}
			for k, v := range command.Metadata {
				bulkPublishMessage[i].Metadata[k] = v
			}
		}
	}
	jsonValue, err := json.Marshal(bulkPublishMessage)
	if err != nil {
		log.Printf("(%s) Error Marshaling: %v", reqID, err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(appResponse{
			Message: err.Error(),
		})
		return
	}
	// publish to dapr
	if protocol == "http" {
		bulkRes, status, err := performBulkPublishHTTP(reqID, pubsubToPublish, topic, jsonValue, reqMetadata)
		if err != nil {
			log.Printf("(%s) BulkPublish failed with error=%v, StatusCode=%d", reqID, err, status)
			log.Printf("(%s) BulkPublish failed with bulkRes errorCode=%v", reqID, bulkRes.ErrorCode)
			for _, stat := range bulkRes.FailedEntries {
				log.Printf("Failed event with entry ID (%s) and error %s", stat.EntryId, stat.Error)
			}

			w.WriteHeader(status)
			json.NewEncoder(w).Encode(bulkRes)
			return
		}
		// pass on status code to the calling application
		w.WriteHeader(status)

		endTime := time.Now()
		duration := endTime.Sub(startTime)
		if status == http.StatusOK || status == http.StatusNoContent {
			log.Printf("(%s) BulkPublish succeeded in %v", reqID, formatDuration(duration))
		} else {
			log.Printf("(%s) BulkPublish failed in %v", reqID, formatDuration(duration))
		}

		json.NewEncoder(w).Encode(bulkRes)
		return
	} else if protocol == "grpc" {
		// Build runtimev1pb.BulkPublishRequestEntry objects
		entries := make([]*runtimev1pb.BulkPublishRequestEntry, 0, len(bulkPublishMessage))
		for _, entry := range bulkPublishMessage {
			e := &runtimev1pb.BulkPublishRequestEntry{
				EntryId:     entry.EntryId,
				ContentType: entry.ContentType,
				Metadata:    entry.Metadata,
			}
			// All data coming into gRPC must be in bytes only
			data, err := daprhttp.ConvertEventToBytes(entry.Event, entry.ContentType)
			if err != nil {
				log.Printf("(%s) Error Marshaling: %v", reqID, err)
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(appResponse{
					Message: err.Error(),
				})
				return
			}
			e.Event = data
			entries = append(entries, e)
		}

		bulkRes, status, err := performBulkPublishGRPC(reqID, pubsubToPublish, topic, entries, reqMetadata)
		log.Printf("status code %d", status)
		if err != nil {
			log.Printf("(%s) BulkPublish failed with error=%v, StatusCode=%d", reqID, err, status)
			log.Printf("(%s) BulkPublish failed with bulkRes errorCode=%v", reqID, bulkRes)
			for _, stat := range bulkRes.GetFailedEntries() {
				log.Printf("Failed event with entry ID (%s) and error %s", stat.GetEntryId(), stat.GetError())
			}

			w.WriteHeader(status)
			json.NewEncoder(w).Encode(bulkRes)
			return
		}
		// pass on status code to the calling application
		w.WriteHeader(status)

		endTime := time.Now()
		duration := endTime.Sub(startTime)
		if status == http.StatusOK || status == http.StatusNoContent {
			log.Printf("(%s) BulkPublish succeeded in %v", reqID, formatDuration(duration))
		} else {
			log.Printf("(%s) BulkPublish failed in %v", reqID, formatDuration(duration))
		}

		json.NewEncoder(w).Encode("Done")
		return
	}
	status := http.StatusBadRequest
	// pass on status code to the calling application
	w.WriteHeader(status)

	endTime := time.Now()
	duration := endTime.Sub(startTime)
	if status == http.StatusOK || status == http.StatusNoContent {
		log.Printf("(%s) BulkPublish succeeded in %v", reqID, formatDuration(duration))
	} else {
		log.Printf("(%s) BulkPublish failed in %v", reqID, formatDuration(duration))
	}

	json.NewEncoder(w).Encode("Done")
}

func performBulkPublishGRPC(reqID string, pubsubToPublish, topic string, entries []*runtimev1pb.BulkPublishRequestEntry, reqMeta map[string]string) (*runtimev1pb.BulkPublishResponse, int, error) {
	url := fmt.Sprintf("localhost:%d", daprPortGRPC)
	log.Printf("Connecting to dapr using url %s", url)

	// change from default pubsub
	req := &runtimev1pb.BulkPublishRequest{
		PubsubName: pubsubToPublish,
		Topic:      topic,
		Metadata:   reqMeta,
		Entries:    entries,
	}
	log.Printf("Pubsub to publish to is %s", req.GetPubsubName())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	res, err := grpcClient.BulkPublishEventAlpha1(ctx, req)
	cancel()
	log.Printf("Error is there? %v", err != nil)
	if err != nil {
		log.Printf("(%s) Publish failed with response : %v", reqID, res)
		log.Printf("(%s) Publish failed: %v", reqID, err)
		return res, http.StatusInternalServerError, err
	}
	return res, http.StatusOK, nil
}

func performBulkPublishHTTP(reqID string, pubsubToPublish, topic string, jsonValue []byte, reqMeta map[string]string) (daprhttp.BulkPublishResponse, int, error) {
	url := fmt.Sprintf("http://localhost:%d/v1.0-alpha1/publish/bulk/%s/%s", daprPortHTTP, pubsubToPublish, topic)
	if len(reqMeta) > 0 {
		params := netUrl.Values{}
		for k, v := range reqMeta {
			params.Set(fmt.Sprintf("metadata.%s", k), v)
		}
		url = url + "?" + params.Encode()
	}

	log.Printf("(%s) BulkPublishing using url %s and body '%s'", reqID, url, jsonValue)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonValue))
	if err != nil {
		return daprhttp.BulkPublishResponse{}, 0, err
	}
	req.Header.Set("Content-Type", "applcation/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		if resp != nil {
			return daprhttp.BulkPublishResponse{}, resp.StatusCode, err
		}
		return daprhttp.BulkPublishResponse{}, http.StatusInternalServerError, err
	}

	// Success scenario, no content is returned.
	if resp.StatusCode == http.StatusNoContent {
		return daprhttp.BulkPublishResponse{}, resp.StatusCode, nil
	}

	defer resp.Body.Close()
	resBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return daprhttp.BulkPublishResponse{}, http.StatusInternalServerError, fmt.Errorf("error read bulkres as bytes %w", err)
	}
	bulkRes := daprhttp.BulkPublishResponse{}
	err = json.Unmarshal(resBytes, &bulkRes)
	if err != nil {
		return daprhttp.BulkPublishResponse{}, http.StatusInternalServerError, fmt.Errorf("error unmarshal bulkres %w", err)
	}

	return bulkRes, resp.StatusCode, nil
}

// when called by the test, this function publishes to dapr
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

	endTime := time.Now()
	duration := endTime.Sub(startTime)
	if status == http.StatusOK || status == http.StatusNoContent {
		log.Printf("(%s) Publish succeeded in %v", reqID, formatDuration(duration))
		resp = appResponse{Message: "Success"}
	} else {
		log.Printf("(%s) Publish failed in %v", reqID, formatDuration(duration))
		resp = appResponse{Message: "Failed"}
	}
	resp.StartTime = epoch(&startTime)
	resp.EndTime = epoch(&endTime)

	json.NewEncoder(w).Encode(resp)
}

func performPublishHTTP(reqID string, topic string, jsonValue []byte, contentType string, metadata map[string]string) (int, error) {
	psName := pubsubName
	if strings.Contains(topic, bulkSubTopicIdentity) {
		psName = pubsubKafka
	}
	url := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/%s", daprPortHTTP, psName, topic)
	if len(metadata) > 0 {
		params := netUrl.Values{}
		for k, v := range metadata {
			params.Set(fmt.Sprintf("metadata.%s", k), v)
		}
		url = url + "?" + params.Encode()
	}

	log.Printf("(%s) Publishing using url %s and body '%s'", reqID, url, jsonValue)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonValue))
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

func performPublishGRPC(reqID string, topic string, jsonValue []byte, contentType string, metadata map[string]string) (int, error) {
	psName := pubsubName
	if strings.Contains(topic, bulkSubTopicIdentity) {
		psName = pubsubKafka
	}
	url := fmt.Sprintf("localhost:%d", daprPortGRPC)
	log.Printf("Connecting to dapr using url %s", url)

	req := &runtimev1pb.PublishEventRequest{
		PubsubName:      psName,
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
	startTime := time.Now()
	reqID := "s-" + uuid.New().String()

	body, err := io.ReadAll(r.Body)
	r.Body.Close()

	if err != nil {
		log.Printf("(%s) Could not read request body: %v", reqID, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	var req callSubscriberMethodRequest
	err = json.Unmarshal(body, &req)
	if err != nil {
		log.Printf("(%s) Could not parse JSON request body: %v", reqID, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if req.ReqID != "" {
		reqID = req.ReqID
	}

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

	duration := time.Now().Sub(startTime)
	log.Printf("(%s) responded in %v via %s", reqID, formatDuration(duration), req.Protocol)
}

func callSubscriberMethodGRPC(reqID, appName, method string) ([]byte, error) {
	invokeReq := &commonv1pb.InvokeRequest{
		Method: method,
	}
	qs := netUrl.Values{"reqid": []string{reqID}}.Encode()
	invokeReq.HttpExtension = &commonv1pb.HTTPExtension{
		Verb:        commonv1pb.HTTPExtension_Verb(commonv1pb.HTTPExtension_Verb_value["POST"]), //nolint:nosnakecase
		Querystring: qs,
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

	return resp.GetData().GetValue(), nil
}

func callSubscriberMethodHTTP(reqID, appName, method string) ([]byte, error) {
	qs := netUrl.Values{"reqid": []string{reqID}}.Encode()
	url := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/%s?%s", daprPortHTTP, appName, method, qs)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer([]byte{}))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
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

// epoch returns the unix epoch timestamp from a time
func epoch(t *time.Time) int {
	return int(t.UnixMilli())
}

// formatDuration formats the duration in ms
func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%dms", d.Truncate(100*time.Microsecond).Milliseconds())
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	log.Printf("Enter appRouter()")
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/tests/publish", performPublish).Methods("POST")
	router.HandleFunc("/tests/bulkpublish", performBulkPublish).Methods("POST")
	router.HandleFunc("/tests/callSubscriberMethod", callSubscriberMethod).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	grpcClient = utils.GetGRPCClient(daprPortGRPC)

	log.Printf("PubSub Publisher - listening on http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
