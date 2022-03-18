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
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/gorilla/mux"
	jsoniter "github.com/json-iterator/go"
	"google.golang.org/grpc"

	daprhttp "github.com/dapr/dapr/pkg/http"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

const (
	appPort = 3000

	// statestore is the name of the store
	stateURLTemplate            = "http://localhost:3500/v1.0/state/%s"
	bulkStateURLTemplate        = "http://localhost:3500/v1.0/state/%s/bulk?metadata.partitionKey=e2etest"
	stateTransactionURLTemplate = "http://localhost:3500/v1.0/state/%s/transaction"
	queryURLTemplate            = "http://localhost:3500/v1.0-alpha1/state/%s/query"

	metadataPartitionKey = "partitionKey"
	partitionKey         = "e2etest"
)

// appState represents a state in this app.
type appState struct {
	Data []byte `json:"data,omitempty"`
}

// daprState represents a state in Dapr.
type daprState struct {
	Key           string            `json:"key,omitempty"`
	Value         *appState         `json:"value,omitempty"`
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
	Key  string      `json:"key"`
	Data interface{} `json:"data"`
	ETag string      `json:"etag"`
}

// requestResponse represents a request or response for the APIs in this app.
type requestResponse struct {
	StartTime int         `json:"start_time,omitempty"`
	EndTime   int         `json:"end_time,omitempty"`
	States    []daprState `json:"states,omitempty"`
	Message   string      `json:"message,omitempty"`
}

type appResponse struct {
	Message string `json:"message,omitempty"`
}

var httpClient = newHTTPClient()

var (
	grpcConn   *grpc.ClientConn
	daprClient runtimev1pb.DaprClient
)

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
	stateURL := fmt.Sprintf(stateURLTemplate, statestore)
	if len(meta) != 0 {
		stateURL += "?" + metadata2RawQuery(meta)
	}
	log.Printf("Posting %d bytes of state to %s", len(data), stateURL)
	res, err := http.Post(stateURL, "application/json", bytes.NewBuffer(data))
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer res.Body.Close()

	// Save must return 204
	if res.StatusCode != http.StatusNoContent {
		err = fmt.Errorf("expected status code 204, got %d", res.StatusCode)
	}
	return res.StatusCode, err
}

func get(key, statestore string, meta map[string]string) (*appState, error) {
	log.Printf("Processing get request for %s.", key)
	url, err := createStateURL(key, statestore, meta)
	if err != nil {
		return nil, err
	}

	log.Printf("Fetching state from %s", url)
	// url is created from user input, it is OK since this is a test app only and will not run in prod.
	/* #nosec */
	res, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("could not get value for key %s from Dapr: %s", key, err.Error())
	}

	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("could not load value for key %s from Dapr: %s", key, err.Error())
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("failed to get value for key %s from Dapr: %s", key, body)
	}

	log.Printf("Found state for key %s: %s", key, body)

	state, err := parseState(key, body)
	if err != nil {
		return nil, err
	}

	return state, nil
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

	var output = make([]daprState, 0, len(states))
	for _, state := range states {
		value, err := get(state.Key, statestore, meta)

		if err != nil {
			return nil, err
		}

		log.Printf("Result for get request for key %s: %v", state.Key, value)
		output = append(output, daprState{
			Key:   state.Key,
			Value: value,
		})
	}

	log.Printf("Result for get request for %d states: %v", len(states), output)
	return output, nil
}

func getBulk(states []daprState, statestore string) ([]daprState, error) {
	log.Printf("Processing get bulk request for %d states.", len(states))

	var output = make([]daprState, 0, len(states))

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

	res, err := http.Post(url, "application/json", bytes.NewBuffer(b))
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

	for _, i := range resp {
		var as appState
		b, err := json.Marshal(i.Data)
		if err != nil {
			return nil, fmt.Errorf("could not marshal return data: %s", err)
		}
		json.Unmarshal(b, &as)

		output = append(output, daprState{
			Key:   i.Key,
			Value: &as,
		})
	}

	log.Printf("Result for bulk get request for %d states: %v", len(states), output)
	return output, nil
}

func delete(key, statestore string, meta map[string]string) error {
	log.Printf("Processing delete request for %s.", key)
	url, err := createStateURL(key, statestore, meta)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("could not create delete request for key %s in Dapr: %s", key, err.Error())
	}

	log.Printf("Deleting state for %s", url)
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not delete key %s in Dapr: %s", key, err.Error())
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("failed to delete key %s in Dapr: %s", key, err.Error())
	}

	return nil
}

func deleteAll(states []daprState, statestore string, meta map[string]string) error {
	log.Printf("Processing delete request for %d states.", len(states))

	for _, state := range states {
		err := delete(state.Key, statestore, meta)

		if err != nil {
			return err
		}
	}

	return nil
}

func executeTransaction(states []daprState, statestore string) error {
	var transactionalOperations []map[string]interface{}
	stateTransactionURL := fmt.Sprintf(stateTransactionURLTemplate, statestore)
	for _, s := range states {
		transactionalOperations = append(transactionalOperations, map[string]interface{}{
			"operation": s.OperationType,
			"request": map[string]interface{}{
				"key":   s.Key,
				"value": s.Value,
			},
		})
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
	res, err := http.Post(stateTransactionURL, "application/json", bytes.NewBuffer(jsonValue))
	if err != nil {
		return err
	}

	res.Body.Close()

	return nil
}

func executeQuery(query []byte, statestore string, meta map[string]string) ([]daprState, error) {
	log.Printf("Processing query request '%s'.", string(query))

	queryURL := fmt.Sprintf(queryURLTemplate, statestore)
	if len(meta) != 0 {
		queryURL += "?" + metadata2RawQuery(meta)
	}
	log.Printf("Posting %d bytes of state to %s", len(query), queryURL)
	resp, err := http.Post(queryURL, "application/json", bytes.NewBuffer(query))
	if err != nil {
		return nil, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("could not load query results from Dapr: %s", err.Error())
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("failed to load query results from Dapr: %s", body)
	}

	var qres daprhttp.QueryResponse
	err = jsoniter.Unmarshal(body, &qres)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal query response from Dapr: %s", err.Error())
	}

	log.Printf("Query returned %d results", len(qres.Results))
	var output = make([]daprState, 0, len(qres.Results))
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
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
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
	if data, err = io.ReadAll(r.Body); err != nil {
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
	var res = requestResponse{}
	var uri = r.URL.RequestURI()
	var statusCode = http.StatusOK

	res.StartTime = epoch()

	cmd := mux.Vars(r)["command"]
	statestore := mux.Vars(r)["statestore"]
	meta := getMetadata(r.URL.Query())
	switch cmd {
	case "save":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		_, err = save(req.States, statestore, meta)
		if err == nil {
			// The save call to dapr side car has returned correct status.
			// Set the status code to statusNoContent
			statusCode = http.StatusNoContent
		}
	case "load":
		if data, err = getRequestBody(w, r); err != nil {
			return
		}
		statusCode, err = load(data, statestore, meta)
	case "get":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		res.States, err = getAll(req.States, statestore, meta)
	case "getbulk":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		res.States, err = getBulk(req.States, statestore)
	case "delete":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		err = deleteAll(req.States, statestore, meta)
		statusCode = http.StatusNoContent
	case "transact":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		err = executeTransaction(req.States, statestore)
	case "query":
		if data, err = getRequestBody(w, r); err != nil {
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

	res.EndTime = epoch()

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
	var err error
	var res requestResponse
	res.StartTime = epoch()
	var statusCode = http.StatusOK

	cmd := mux.Vars(r)["command"]
	statestore := mux.Vars(r)["statestore"]
	meta := getMetadata(r.URL.Query())
	switch cmd {
	case "save":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		_, err := daprClient.SaveState(context.Background(), &runtimev1pb.SaveStateRequest{
			StoreName: statestore,
			States:    daprState2StateItems(req.States, meta),
		})
		statusCode = http.StatusNoContent
		if err != nil {
			statusCode, res.Message = setErrorMessage("ExecuteSaveState", err.Error())
		}
	case "getbulk":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		response, err := daprClient.GetBulkState(context.Background(), &runtimev1pb.GetBulkStateRequest{
			StoreName: statestore,
			Keys:      daprState2Keys(req.States),
			Metadata:  map[string]string{metadataPartitionKey: partitionKey},
		})
		if err != nil {
			statusCode, res.Message = setErrorMessage("GetBulkState", err.Error())
		}
		states, err := toDaprStates(response)
		if err != nil {
			statusCode, res.Message = setErrorMessage("GetBulkState", err.Error())
		}
		res.States = states
	case "get":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		states, err := getAllGRPC(req.States, statestore, meta)
		if err != nil {
			statusCode, res.Message = setErrorMessage("GetState", err.Error())
		}
		res.States = states
	case "delete":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		statusCode = http.StatusNoContent
		err = deleteAllGRPC(req.States, statestore, meta)
		if err != nil {
			statusCode, res.Message = setErrorMessage("DeleteState", err.Error())
		}
	case "transact":
		if req, err = parseRequestBody(w, r); err != nil {
			return
		}
		_, err = daprClient.ExecuteStateTransaction(context.Background(), &runtimev1pb.ExecuteStateTransactionRequest{
			StoreName:  statestore,
			Operations: daprState2TransactionalStateRequest(req.States),
			Metadata:   map[string]string{metadataPartitionKey: partitionKey},
		})
		if err != nil {
			statusCode, res.Message = setErrorMessage("ExecuteStateTransaction", err.Error())
		}
	case "query":
		if data, err = getRequestBody(w, r); err != nil {
			return
		}
		resp, err := daprClient.QueryStateAlpha1(context.Background(), &runtimev1pb.QueryStateRequest{
			StoreName: statestore,
			Query:     string(data),
			Metadata:  meta,
		})
		if err != nil {
			statusCode, res.Message = setErrorMessage("QueryState", err.Error())
		}
		res.States = make([]daprState, 0, len(resp.Results))
		for _, r := range resp.Results {
			res.States = append(res.States, daprState{
				Key:   r.Key,
				Value: &appState{Data: r.Data},
			})
		}
	default:
		statusCode = http.StatusInternalServerError
		unsupportedCommandMessage := fmt.Sprintf("GRPC protocol command %s not supported", cmd)
		log.Printf(unsupportedCommandMessage)
		res.Message = unsupportedCommandMessage
	}

	res.EndTime = epoch()
	if statusCode != http.StatusOK || statusCode != http.StatusNoContent {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
}

func daprState2Keys(states []daprState) []string {
	var keys []string
	for _, state := range states {
		keys = append(keys, state.Key)
	}
	return keys
}

func toDaprStates(response *runtimev1pb.GetBulkStateResponse) ([]daprState, error) {
	var result []daprState
	for _, state := range response.Items {
		if state.Error != "" {
			return nil, fmt.Errorf("%s while getting bulk state", state.Error)
		}
		daprStateItem, err := parseState(state.Key, state.Data)
		if err != nil {
			return nil, err
		}
		result = append(result, daprState{
			Key:   state.Key,
			Value: daprStateItem,
		})
	}

	return result, nil
}

func deleteAllGRPC(states []daprState, statestore string, meta map[string]string) error {
	m := map[string]string{metadataPartitionKey: partitionKey}
	for k, v := range meta {
		m[k] = v
	}
	for _, state := range states {
		log.Printf("deleting sate for key %s\n", state.Key)
		_, err := daprClient.DeleteState(context.Background(), &runtimev1pb.DeleteStateRequest{
			StoreName: statestore,
			Key:       state.Key,
			Metadata:  m,
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func getAllGRPC(states []daprState, statestore string, meta map[string]string) ([]daprState, error) {
	var responses []daprState
	m := map[string]string{metadataPartitionKey: partitionKey}
	for k, v := range meta {
		m[k] = v
	}
	for _, state := range states {
		log.Printf("getting state for key %s\n", state.Key)
		res, err := daprClient.GetState(context.Background(), &runtimev1pb.GetStateRequest{
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
		responses = append(responses, daprState{
			Key:   state.Key,
			Value: val,
		})
	}

	return responses, nil
}

func setErrorMessage(method, errorString string) (int, string) {
	log.Printf("GRPC %s had error %s\n", method, errorString)

	return http.StatusInternalServerError, errorString
}

func daprState2StateItems(daprStates []daprState, meta map[string]string) []*commonv1pb.StateItem {
	var stateItems []*commonv1pb.StateItem
	m := map[string]string{metadataPartitionKey: partitionKey}
	for k, v := range meta {
		m[k] = v
	}
	for _, daprState := range daprStates {
		val, _ := json.Marshal(daprState.Value)
		stateItems = append(stateItems, &commonv1pb.StateItem{
			Key:      daprState.Key,
			Value:    val,
			Metadata: m,
		})
	}

	return stateItems
}

func daprState2TransactionalStateRequest(daprStates []daprState) []*runtimev1pb.TransactionalStateOperation {
	var transactionalStateRequests []*runtimev1pb.TransactionalStateOperation
	for _, daprState := range daprStates {
		val, _ := json.Marshal(daprState.Value)
		transactionalStateRequests = append(transactionalStateRequests, &runtimev1pb.TransactionalStateOperation{
			OperationType: daprState.OperationType,
			Request: &commonv1pb.StateItem{
				Key:   daprState.Key,
				Value: val,
			},
		})
	}

	return transactionalStateRequests
}

func createStateURL(key, statestore string, meta map[string]string) (string, error) {
	stateURL := fmt.Sprintf(stateURLTemplate, statestore)
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
	bulkStateURL := fmt.Sprintf(bulkStateURLTemplate, statestore)
	url, err := url.Parse(bulkStateURL)
	if err != nil {
		return "", fmt.Errorf("could not parse %s: %s", bulkStateURL, err.Error())
	}

	return url.String(), nil
}

// epoch returns the current unix epoch timestamp
func epoch() int {
	return (int)(time.Now().UTC().UnixNano() / 1000000)
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/test/http/{command}/{statestore}", httpHandler).Methods("POST")
	router.HandleFunc("/test/grpc/{command}/{statestore}", grpcHandler).Methods("POST")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func newHTTPClient() http.Client {
	dialer := &net.Dialer{ //nolint:exhaustivestruct
		Timeout: 5 * time.Second,
	}
	netTransport := &http.Transport{ //nolint:exhaustivestruct
		DialContext:         dialer.DialContext,
		TLSHandshakeTimeout: 5 * time.Second,
	}

	return http.Client{ //nolint:exhaustivestruct
		Timeout:   30 * time.Second,
		Transport: netTransport,
	}
}

func initGRPCClient() {
	daprPort, _ := os.LookupEnv("DAPR_GRPC_PORT")
	url := fmt.Sprintf("localhost:%s", daprPort)
	log.Printf("Connecting to dapr using url %s", url)
	for retries := 10; retries > 0; retries-- {
		var err error
		grpcConn, err = grpc.Dial(url, grpc.WithInsecure())
		if err == nil {
			break
		}

		if retries == 0 {
			log.Printf("Could not connect to dapr: %v", err)
			log.Panic(err)
		}

		log.Printf("Could not connect to dapr: %v, retrying...", err)
		time.Sleep(5 * time.Second)
	}

	daprClient = runtimev1pb.NewDaprClient(grpcConn)
}

func main() {
	initGRPCClient()

	log.Printf("State App - listening on http://localhost:%d", appPort)
	log.Printf("State endpoint - to be saved at %s", fmt.Sprintf(stateURLTemplate, "statestore"))

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
