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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	daprhttp "github.com/dapr/dapr/pkg/http"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	// statestore is the name of the store
	stateURLTemplate            = "http://localhost:%d/v1.0/state/%s"
	bulkStateURLTemplate        = "http://localhost:%d/v1.0/state/%s/bulk?metadata.partitionKey=e2etest"
	stateTransactionURLTemplate = "http://localhost:%d/v1.0/state/%s/transaction"
	queryURLTemplate            = "http://localhost:%d/v1.0-alpha1/state/%s/query"

	metadataPartitionKey = "partitionKey"
	partitionKey         = "e2etest"
	badEtag              = "99999" // Must be numeric because of Redis
)

var (
	appPort      = 3000
	daprGRPCPort = 50001
	daprHTTPPort = 3500

	httpClient = utils.NewHTTPClient()
	grpcClient runtimev1pb.DaprClient
)

func init() {
	p := os.Getenv("DAPR_HTTP_PORT")
	if p != "" && p != "0" {
		daprHTTPPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("DAPR_GRPC_PORT")
	if p != "" && p != "0" {
		daprGRPCPort, _ = strconv.Atoi(p)
	}
	p = os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
}

// appState represents a state in this app.
type appState struct {
	Data []byte `json:"data,omitempty"`
}

// daprState represents a state in Dapr.
type daprState struct {
	Key           string            `json:"key,omitempty"`
	Value         *appState         `json:"value,omitempty"`
	Etag          string            `json:"etag,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	OperationType string            `json:"operationType,omitempty"`
}

// bulkGetRequest is the bulk get request object for the test
type bulkGetRequest struct {
	Metadata    map[string]string `json:"metadata"`
	Keys        []string          `json:"keys"`
	Parallelism int               `json:"parallelism"`
}

// bulkGetResponse is the response object from Dapr for a bulk get operation.
type bulkGetResponse struct {
	Key  string `json:"key"`
	Data any    `json:"data"`
	ETag string `json:"etag"`
}

// requestResponse represents a request or response for the APIs in this app.
type requestResponse struct {
	States  []daprState `json:"states,omitempty"`
	Message string      `json:"message,omitempty"`
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

func save(states []daprState, statestore string, meta map[string]string) (int, error) {
	log.Printf("Processing save request for %d entries.", len(states))

	jsonValue, err := json.Marshal(states)
	if err != nil {
		log.Printf("Could save states in Dapr: %s", err.Error())
		return http.StatusInternalServerError, err
	}

	return load(jsonValue, statestore, meta)
}

func load(data []byte, statestore string, meta map[string]string) (int, error) {
	stateURL := fmt.Sprintf(stateURLTemplate, daprHTTPPort, statestore)
	if len(meta) != 0 {
		stateURL += "?" + metadata2RawQuery(meta)
	}
	log.Printf("Posting %d bytes of state to %s", len(data), stateURL)
	res, err := httpClient.Post(stateURL, "application/json", bytes.NewReader(data))
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer res.Body.Close()

	// Save must return 204
	if res.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(res.Body)
		err = fmt.Errorf("expected status code 204, got %d; response: %s", res.StatusCode, string(body))
	}

	// Drain before closing
	_, _ = io.Copy(io.Discard, res.Body)

	return res.StatusCode, err
}

func get(key string, statestore string, meta map[string]string) (*appState, string, error) {
	log.Printf("Processing get request for %s.", key)
	url, err := createStateURL(key, statestore, meta)
	if err != nil {
		return nil, "", err
	}

	log.Printf("Fetching state from %s", url)
	// url is created from user input, it is OK since this is a test app only and will not run in prod.
	/* #nosec */
	res, err := httpClient.Get(url)
	if err != nil {
		return nil, "", fmt.Errorf("could not get value for key %s from Dapr: %s", key, err.Error())
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, "", fmt.Errorf("could not load value for key %s from Dapr: %s", key, err.Error())
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, "", fmt.Errorf("failed to get value for key %s from Dapr: %s", key, body)
	}

	log.Printf("Found state for key %s: %s", key, body)

	state, err := parseState(key, body)
	if err != nil {
		return nil, "", err
	}

	return state, res.Header.Get("etag"), nil
}

func parseState(key string, body []byte) (*appState, error) {
	state := &appState{}
	if len(body) == 0 {
		return nil, nil
	}

	// a key not found in Dapr will return 200 but an empty response.
	err := json.Unmarshal(body, &state)
	if err != nil {
		var stateData string
		stringMarshalErr := json.Unmarshal(body, &stateData)
		if stringMarshalErr != nil {
			return nil, fmt.Errorf("could not parse value for key %s from Dapr: %s", key, err.Error())
		}
		state.Data = []byte(stateData)
	}
	return state, nil
}

func getAll(states []daprState, statestore string, meta map[string]string) ([]daprState, error) {
	log.Printf("Processing get request for %d states.", len(states))

	output := make([]daprState, 0, len(states))
	for _, state := range states {
		value, etag, err := get(state.Key, statestore, meta)
		if err != nil {
			return nil, err
		}

		log.Printf("Result for get request for key %s: %v", state.Key, value)
		output = append(output, daprState{
			Key:   state.Key,
			Value: value,
			Etag:  etag,
		})
	}

	log.Printf("Result for get request for %d states: %v", len(states), output)
	return output, nil
}

func getBulk(states []daprState, statestore string) ([]daprState, error) {
	log.Printf("Processing get bulk request for %d states.", len(states))

	output := make([]daprState, 0, len(states))

	url, err := createBulkStateURL(statestore)
	if err != nil {
		return nil, err
	}
	log.Printf("Fetching bulk state from %s", url)

	req := bulkGetRequest{}
	for _, s := range states {
		req.Keys = append(req.Keys, s.Key)
	}

	b, err := json.Marshal(&req)
	if err != nil {
		return nil, err
	}

	res, err := httpClient.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("could not load values for bulk get from Dapr: %s", err.Error())
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("failed to load values for bulk get from Dapr: %s", body)
	}

	var resp []bulkGetResponse
	err = json.Unmarshal(body, &resp)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal bulk get response from Dapr: %s", err.Error())
	}

	for _, state := range resp {
		var as appState
		b, err := json.Marshal(state.Data)
		if err != nil {
			return nil, fmt.Errorf("could not marshal return data: %s", err)
		}
		json.Unmarshal(b, &as)

		output = append(output, daprState{
			Key:   state.Key,
			Value: &as,
			Etag:  state.ETag,
		})
	}

	log.Printf("Result for bulk get request for %d states: %v", len(states), output)
	return output, nil
}

func delete(key, statestore string, meta map[string]string, etag string) (int, error) {
	log.Printf("Processing delete request for %s", key)
	url, err := createStateURL(key, statestore, meta)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		return 0, fmt.Errorf("could not create delete request for key %s in Dapr: %s", key, err.Error())
	}
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}

	log.Printf("Deleting state for %s", url)
	res, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("could not delete key %s in Dapr: %s", key, err.Error())
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, fmt.Errorf("failed to delete key %s in Dapr: %s", key, string(body))
	}

	// Drain before closing
	_, _ = io.Copy(io.Discard, res.Body)

	return res.StatusCode, nil
}

func deleteAll(states []daprState, statestore string, meta map[string]string) error {
	log.Printf("Processing delete request for %d states.", len(states))

	for _, state := range states {
		_, err := delete(state.Key, statestore, meta, "")
		if err != nil {
			return err
		}
	}

	return nil
}

func executeTransaction(states []daprState, statestore string) error {
	transactionalOperations := make([]map[string]interface{}, len(states))
	stateTransactionURL := fmt.Sprintf(stateTransactionURLTemplate, daprHTTPPort, statestore)
	for i, s := range states {
		transactionalOperations[i] = map[string]interface{}{
			"operation": s.OperationType,
			"request": map[string]interface{}{
				"key":   s.Key,
				"value": s.Value,
			},
		}
	}

	jsonValue, err := json.Marshal(map[string]interface{}{
		"operations": transactionalOperations,
		"metadata":   map[string]string{metadataPartitionKey: partitionKey},
	})
	if err != nil {
		log.Printf("Could save transactional operations in Dapr: %s", err.Error())
		return err
	}

	log.Printf("Posting state to %s with '%s'", stateTransactionURL, jsonValue)
	res, err := httpClient.Post(stateTransactionURL, "application/json", bytes.NewReader(jsonValue))
	if err != nil {
		return err
	}

	res.Body.Close()

	return nil
}

func executeQuery(query []byte, statestore string, meta map[string]string) ([]daprState, error) {
	log.Printf("Processing query request '%s'.", string(query))

	queryURL := fmt.Sprintf(queryURLTemplate, daprHTTPPort, statestore)
	if len(meta) != 0 {
		queryURL += "?" + metadata2RawQuery(meta)
	}
	log.Printf("Posting %d bytes of state to %s", len(query), queryURL)
	resp, err := httpClient.Post(queryURL, "application/json", bytes.NewReader(query))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not load query results from Dapr: %s", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("failed to load query results from Dapr: %s", body)
	}

	var qres daprhttp.QueryResponse
	err = json.Unmarshal(body, &qres)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal query response from Dapr: %v. Raw response: '%s'", err, string(body))
	}

	log.Printf("Query returned %d results", len(qres.Results))
	output := make([]daprState, 0, len(qres.Results))
	for _, item := range qres.Results {
		output = append(output, daprState{
			Key: item.Key,
			Value: &appState{
				Data: item.Data,
			},
		})
	}

	log.Printf("Result for query request for %d states: %v", len(output), output)
	return output, nil
}

func parseRequestBody(w http.ResponseWriter, r *http.Request) (*requestResponse, error) {
	req := &requestResponse{}
	err := json.NewDecoder(r.Body).Decode(req)
	if err != nil {
		log.Printf("Could not parse request body: %s", err.Error())
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(requestResponse{
			Message: err.Error(),
		})
		return nil, err
	}
	for i := range req.States {
		req.States[i].Metadata = map[string]string{metadataPartitionKey: partitionKey}
	}
	log.Printf("%v\n", *req)
	return req, nil
}

func getRequestBody(w http.ResponseWriter, r *http.Request) (data []byte, err error) {
	data, err = io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Could not read request body: %s", err.Error())
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(requestResponse{
			Message: err.Error(),
		})
	}
	return
}

func getMetadata(values url.Values) map[string]string {
	ret := make(map[string]string)

	for k, v := range values {
		ret[k] = v[0]
	}

	return ret
}

func metadata2RawQuery(meta map[string]string) string {
	if len(meta) == 0 {
		return ""
	}
	arr := make([]string, 0, len(meta))
	for k, v := range meta {
		arr = append(arr, fmt.Sprintf("metadata.%s=%s", k, v))
	}
	return strings.Join(arr, "&")
}

// handles all APIs for HTTP calls
func httpHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing request for %s", r.URL.RequestURI())

	var req *requestResponse
	var data []byte
	var err error
	res := requestResponse{}
	uri := r.URL.RequestURI()
	statusCode := http.StatusOK

	cmd := mux.Vars(r)["command"]
	statestore := mux.Vars(r)["statestore"]
	meta := getMetadata(r.URL.Query())
	switch cmd {
	case "save":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		_, err = save(req.States, statestore, meta)
		if err == nil {
			// The save call to dapr side car has returned correct status.
			// Set the status code to statusNoContent
			statusCode = http.StatusNoContent
		}
	case "load":
		data, err = getRequestBody(w, r)
		if err != nil {
			return
		}
		statusCode, err = load(data, statestore, meta)
	case "get":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		res.States, err = getAll(req.States, statestore, meta)
	case "getbulk":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		res.States, err = getBulk(req.States, statestore)
	case "delete":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		err = deleteAll(req.States, statestore, meta)
		statusCode = http.StatusNoContent
	case "transact":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		err = executeTransaction(req.States, statestore)
	case "query":
		data, err = getRequestBody(w, r)
		if err != nil {
			return
		}
		res.States, err = executeQuery(data, statestore, meta)
	default:
		err = fmt.Errorf("invalid URI: %s", uri)
		statusCode = http.StatusBadRequest
		res.Message = err.Error()
	}
	statusCheck := (statusCode == http.StatusOK || statusCode == http.StatusNoContent)
	if err != nil && statusCheck {
		log.Printf("Error: %v", err)
		statusCode = http.StatusInternalServerError
		res.Message = err.Error()
	}

	if !statusCheck {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
}

// Handles all APIs for GRPC
func grpcHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Processing request for ", r.URL.RequestURI())
	var req *requestResponse
	var data []byte
	var states []daprState
	var err error
	var res requestResponse
	var response *runtimev1pb.GetBulkStateResponse
	statusCode := http.StatusOK

	cmd := mux.Vars(r)["command"]
	statestore := mux.Vars(r)["statestore"]
	meta := getMetadata(r.URL.Query())
	switch cmd {
	case "save":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		_, err = grpcClient.SaveState(context.Background(), &runtimev1pb.SaveStateRequest{
			StoreName: statestore,
			States:    daprState2StateItems(req.States, meta),
		})
		statusCode = http.StatusNoContent
		if err != nil {
			statusCode, res.Message = setErrorMessage("ExecuteSaveState", err.Error())
		}
	case "getbulk":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		response, err = grpcClient.GetBulkState(context.Background(), &runtimev1pb.GetBulkStateRequest{
			StoreName: statestore,
			Keys:      daprState2Keys(req.States),
			Metadata:  map[string]string{metadataPartitionKey: partitionKey},
		})
		if err != nil {
			statusCode, res.Message = setErrorMessage("GetBulkState", err.Error())
		}
		states, err = toDaprStates(response)
		if err != nil {
			statusCode, res.Message = setErrorMessage("GetBulkState", err.Error())
		}
		res.States = states
	case "get":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		states, err = getAllGRPC(req.States, statestore, meta)
		if err != nil {
			statusCode, res.Message = setErrorMessage("GetState", err.Error())
		}
		res.States = states
	case "delete":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		statusCode = http.StatusNoContent
		err = deleteAllGRPC(req.States, statestore, meta)
		if err != nil {
			statusCode, res.Message = setErrorMessage("DeleteState", err.Error())
		}
	case "transact":
		req, err = parseRequestBody(w, r)
		if err != nil {
			return
		}
		_, err = grpcClient.ExecuteStateTransaction(context.Background(), &runtimev1pb.ExecuteStateTransactionRequest{
			StoreName:  statestore,
			Operations: daprState2TransactionalStateRequest(req.States),
			Metadata:   map[string]string{metadataPartitionKey: partitionKey},
		})
		if err != nil {
			statusCode, res.Message = setErrorMessage("ExecuteStateTransaction", err.Error())
		}
	case "query":
		data, err = getRequestBody(w, r)
		if err != nil {
			return
		}
		resp, err := grpcClient.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
			StoreName: statestore,
			Query:     string(data),
			Metadata:  meta,
		})
		if err != nil {
			statusCode, res.Message = setErrorMessage("QueryState", err.Error())
		}
		if resp != nil && len(resp.Results) > 0 {
			res.States = make([]daprState, 0, len(resp.Results))
			for _, r := range resp.Results {
				res.States = append(res.States, daprState{
					Key:   r.Key,
					Value: &appState{Data: r.Data},
				})
			}
		}
	default:
		statusCode = http.StatusInternalServerError
		unsupportedCommandMessage := fmt.Sprintf("GRPC protocol command %s not supported", cmd)
		log.Print(unsupportedCommandMessage)
		res.Message = unsupportedCommandMessage
	}

	if statusCode != http.StatusOK && statusCode != http.StatusNoContent {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
}

func daprState2Keys(states []daprState) []string {
	keys := make([]string, len(states))
	for i, state := range states {
		keys[i] = state.Key
	}
	return keys
}

func toDaprStates(response *runtimev1pb.GetBulkStateResponse) ([]daprState, error) {
	result := make([]daprState, len(response.Items))
	for i, state := range response.Items {
		if state.Error != "" {
			return nil, fmt.Errorf("%s while getting bulk state", state.Error)
		}
		daprStateItem, err := parseState(state.Key, state.Data)
		if err != nil {
			return nil, err
		}
		result[i] = daprState{
			Key:   state.Key,
			Value: daprStateItem,
			Etag:  state.Etag,
		}
	}

	return result, nil
}

func deleteAllGRPC(states []daprState, statestore string, meta map[string]string) (err error) {
	if len(states) == 0 {
		return nil
	}

	if len(states) == 1 {
		log.Print("deleting sate for key", states[0].Key)
		m := map[string]string{metadataPartitionKey: partitionKey}
		for k, v := range meta {
			m[k] = v
		}
		_, err = grpcClient.DeleteState(context.Background(), &runtimev1pb.DeleteStateRequest{
			StoreName: statestore,
			Key:       states[0].Key,
			Metadata:  m,
		})
		return err
	}

	keys := make([]string, len(states))
	for i, state := range states {
		keys[i] = state.Key
	}
	log.Print("deleting bulk sates for keys", keys)
	_, err = grpcClient.DeleteBulkState(context.Background(), &runtimev1pb.DeleteBulkStateRequest{
		StoreName: statestore,
		States:    daprState2StateItems(states, meta),
	})
	return err
}

func getAllGRPC(states []daprState, statestore string, meta map[string]string) ([]daprState, error) {
	m := map[string]string{metadataPartitionKey: partitionKey}
	for k, v := range meta {
		m[k] = v
	}
	responses := make([]daprState, len(states))
	for i, state := range states {
		log.Printf("getting state for key %s\n", state.Key)
		res, err := grpcClient.GetState(context.Background(), &runtimev1pb.GetStateRequest{
			StoreName: statestore,
			Key:       state.Key,
			Metadata:  m,
		})
		if err != nil {
			return nil, err
		}
		log.Printf("found state for key %s, value is %s\n", state.Key, res.Data)
		val, err := parseState(state.Key, res.Data)
		if err != nil {
			return nil, err
		}
		responses[i] = daprState{
			Key:   state.Key,
			Value: val,
		}
	}

	return responses, nil
}

func setErrorMessage(method, errorString string) (int, string) {
	log.Printf("GRPC %s had error %s", method, errorString)
	return http.StatusInternalServerError, errorString
}

func daprState2StateItems(daprStates []daprState, meta map[string]string) []*commonv1pb.StateItem {
	m := map[string]string{metadataPartitionKey: partitionKey}
	for k, v := range meta {
		m[k] = v
	}
	stateItems := make([]*commonv1pb.StateItem, len(daprStates))
	for i, daprState := range daprStates {
		val, _ := json.Marshal(daprState.Value)
		stateItems[i] = &commonv1pb.StateItem{
			Key:      daprState.Key,
			Value:    val,
			Metadata: m,
		}
		if daprState.Etag != "" {
			stateItems[i].Etag = &commonv1pb.Etag{
				Value: daprState.Etag,
			}
		}
	}

	return stateItems
}

func daprState2TransactionalStateRequest(daprStates []daprState) []*runtimev1pb.TransactionalStateOperation {
	transactionalStateRequests := make([]*runtimev1pb.TransactionalStateOperation, len(daprStates))
	for i, daprState := range daprStates {
		val, _ := json.Marshal(daprState.Value)
		transactionalStateRequests[i] = &runtimev1pb.TransactionalStateOperation{
			OperationType: daprState.OperationType,
			Request: &commonv1pb.StateItem{
				Key:   daprState.Key,
				Value: val,
			},
		}
	}

	return transactionalStateRequests
}

func createStateURL(key, statestore string, meta map[string]string) (string, error) {
	stateURL := fmt.Sprintf(stateURLTemplate, daprHTTPPort, statestore)
	url, err := url.Parse(stateURL)
	if err != nil {
		return "", fmt.Errorf("could not parse %s: %s", stateURL, err.Error())
	}

	url.Path = path.Join(url.Path, key)

	m := map[string]string{metadataPartitionKey: partitionKey}
	for k, v := range meta {
		m[k] = v
	}
	url.RawQuery = metadata2RawQuery(m)

	return url.String(), nil
}

func createBulkStateURL(statestore string) (string, error) {
	bulkStateURL := fmt.Sprintf(bulkStateURLTemplate, daprHTTPPort, statestore)
	url, err := url.Parse(bulkStateURL)
	if err != nil {
		return "", fmt.Errorf("could not parse %s: %s", bulkStateURL, err.Error())
	}

	return url.String(), nil
}

// Handles the etag test for HTTP
func etagTestHTTPHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing request for %s", r.URL.RequestURI())

	statestore := mux.Vars(r)["statestore"]

	pkMetadata := map[string]string{metadataPartitionKey: partitionKey}

	testErr := func() error {
		// Use two random keys for testing
		var (
			value, newValue *appState
			newEtag         string
			etags           [2]string
		)
		keys := [2]string{
			uuid.NewString(),
			uuid.NewString(),
		}

		// First, write two values
		_, err := save([]daprState{
			{Key: keys[0], Value: &appState{Data: []byte("1")}, Metadata: pkMetadata},
			{Key: keys[1], Value: &appState{Data: []byte("1")}, Metadata: pkMetadata},
		}, statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to store initial values: %w", err)
		}

		// Retrieve the two values to get the etag
		value, etags[0], err = get(keys[0], statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to retrieve initial value 0: %w", err)
		}
		if value == nil || string(value.Data) != "1" {
			return fmt.Errorf("invalid value for state 0 initial value: %#v", value)
		}
		if etags[0] == "" {
			return errors.New("etag is empty for state 0 initial value")
		}

		value, etags[1], err = get(keys[1], statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to retrieve initial value 1: %w", err)
		}
		if value == nil || string(value.Data) != "1" {
			return fmt.Errorf("invalid value for state 1 initial value: %#v", value)
		}
		if etags[1] == "" {
			return errors.New("etag is empty for state 1 initial value")
		}

		// Update the first state using the correct etag
		_, err = save([]daprState{
			{Key: keys[0], Value: &appState{Data: []byte("2")}, Metadata: pkMetadata, Etag: etags[0]},
		}, statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to update value 0: %w", err)
		}

		// Check the first state
		newValue, newEtag, err = get(keys[0], statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to retrieve updated value 0: %w", err)
		}
		if newValue == nil || string(newValue.Data) != "2" {
			return fmt.Errorf("invalid value for state 0 updated value: %#v", value)
		}
		if newEtag == "" || newEtag == etags[0] {
			return fmt.Errorf("etag is empty or invalid for updated state 0: %s", newEtag)
		}
		etags[0] = newEtag

		// Updating with wrong etag should fail with 409 status code
		statusCode, _ := save([]daprState{
			{Key: keys[1], Value: &appState{Data: []byte("2")}, Metadata: pkMetadata, Etag: badEtag},
		}, statestore, pkMetadata)
		if statusCode != http.StatusConflict {
			return fmt.Errorf("expected update with invalid etag to fail with status code 409, but got: %d", statusCode)
		}

		// Value should not have changed
		newValue, newEtag, err = get(keys[1], statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to retrieve (not) updated value 1: %w", err)
		}
		if newValue == nil || string(newValue.Data) != "1" {
			return fmt.Errorf("invalid value for state 1 (not) updated value: %#v", value)
		}
		if newEtag == "" || newEtag != etags[1] {
			return fmt.Errorf("etag is empty or invalid for updated state 1: %s", newEtag)
		}

		// Bulk update with all valid etags
		_, err = save([]daprState{
			{Key: keys[0], Value: &appState{Data: []byte("3")}, Metadata: pkMetadata, Etag: etags[0]},
			{Key: keys[1], Value: &appState{Data: []byte("3")}, Metadata: pkMetadata, Etag: etags[1]},
		}, statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to update bulk values: %w", err)
		}

		// Retrieve the two values to confirm they're updated
		newValue, newEtag, err = get(keys[0], statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to retrieve updated value 0: %w", err)
		}
		if newValue == nil || string(newValue.Data) != "3" {
			return fmt.Errorf("invalid value for state 0 updated value: %#v", newValue)
		}
		if newEtag == "" || newEtag == etags[0] {
			return fmt.Errorf("etag is empty or invalid for updated state 0: %s", newEtag)
		}
		etags[0] = newEtag

		newValue, newEtag, err = get(keys[1], statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to retrieve updated value 1: %w", err)
		}
		if newValue == nil || string(newValue.Data) != "3" {
			return fmt.Errorf("invalid value for state 1 updated value: %#v", newValue)
		}
		if newEtag == "" || newEtag == etags[1] {
			return fmt.Errorf("etag is empty or invalid for updated state 1: %s", newEtag)
		}
		etags[1] = newEtag

		// Bulk update with one etag incorrect
		statusCode, _ = save([]daprState{
			{Key: keys[0], Value: &appState{Data: []byte("4")}, Metadata: pkMetadata, Etag: badEtag},
			{Key: keys[1], Value: &appState{Data: []byte("4")}, Metadata: pkMetadata, Etag: etags[1]},
		}, statestore, pkMetadata)
		if statusCode != http.StatusConflict {
			return fmt.Errorf("expected update with invalid etag to fail with status code 409, but got: %d", statusCode)
		}

		// Retrieve the two values to confirm only the second is updated
		newValue, newEtag, err = get(keys[0], statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to retrieve (not) updated value 0: %w", err)
		}
		if newValue == nil || string(newValue.Data) != "3" {
			return fmt.Errorf("invalid value for state 0 (not) updated value: %#v", value)
		}
		if newEtag == "" || newEtag != etags[0] {
			return fmt.Errorf("etag is empty or invalid for (not) updated state 0: %s", newEtag)
		}

		newValue, newEtag, err = get(keys[1], statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to retrieve updated value 1: %w", err)
		}
		if newValue == nil || string(newValue.Data) != "4" {
			return fmt.Errorf("invalid value for state 1 updated value: %#v", newValue)
		}
		if newEtag == "" || newEtag == etags[1] {
			return fmt.Errorf("etag is empty or invalid for updated state 1: %s", newEtag)
		}
		etags[1] = newEtag

		// Delete single item with incorrect etag
		statusCode, _ = delete(keys[0], statestore, pkMetadata, badEtag)
		if statusCode != http.StatusConflict {
			return fmt.Errorf("expected delete with invalid etag to fail with status code 409, but got: %d", statusCode)
		}

		// Value should not have changed
		newValue, newEtag, err = get(keys[0], statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to retrieve (not) updated value 0: %w", err)
		}
		if newValue == nil || string(newValue.Data) != "3" {
			return fmt.Errorf("invalid value for state 0 (not) updated value: %#v", value)
		}
		if newEtag == "" || newEtag != etags[0] {
			return fmt.Errorf("etag is empty or invalid for (not) updated state 0: %s", newEtag)
		}

		// TODO: There's no "Bulk Delete" API in HTTP right now, so we can't test that
		// Create a test here when the API is implemented
		err = deleteAll([]daprState{
			{Key: keys[0], Metadata: pkMetadata},
			{Key: keys[1], Metadata: pkMetadata},
		}, statestore, pkMetadata)
		if err != nil {
			return fmt.Errorf("failed to delete all data at the end of the test: %w", err)
		}

		return nil
	}()

	w.Header().Add("content-type", "application/json")
	if testErr != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]any{
			"error": testErr.Error(),
		})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/http/{command}/{statestore}", httpHandler).Methods("POST")
	router.HandleFunc("/test/grpc/{command}/{statestore}", grpcHandler).Methods("POST")
	router.HandleFunc("/test-etag/http/{statestore}", etagTestHTTPHandler).Methods("POST")
	// router.HandleFunc("/test-etag/grpc/{statestore}", etagTestHTTPHandler).Methods("POST")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	grpcClient = utils.GetGRPCClient(daprGRPCPort)

	log.Printf("State App - listening on http://localhost:%d", appPort)
	log.Printf("State endpoint - to be saved at %s", fmt.Sprintf(stateURLTemplate, daprHTTPPort, "statestore"))
	utils.StartServer(appPort, appRouter, true, false)
}
