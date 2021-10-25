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
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"time"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"

	"github.com/gorilla/mux"
	"google.golang.org/grpc"
)

const (
	appPort = 3000

	// statestore is the name of the store
	stateURLTemplate            = "http://localhost:3500/v1.0/state/%s"
	bulkStateURLTemplate        = "http://localhost:3500/v1.0/state/%s/bulk?metadata.partitionKey=e2etest"
	stateTransactionURLTemplate = "http://localhost:3500/v1.0/state/%s/transaction"

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

func save(states []daprState, statestore string) error {
	log.Printf("Processing save request for %d entries.", len(states))

	jsonValue, err := json.Marshal(states)
	if err != nil {
		log.Printf("Could save states in Dapr: %s", err.Error())
		return err
	}

	stateURL := fmt.Sprintf(stateURLTemplate, statestore)
	log.Printf("Posting %d bytes of state to %s", len(jsonValue), stateURL)
	res, err := http.Post(stateURL, "application/json", bytes.NewBuffer(jsonValue))
	if err != nil {
		return err
	}

	defer res.Body.Close()
	// Save must return 201
	if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("expected status code 204, got %d", res.StatusCode)
	}
	return nil
}

func get(key, statestore string) (*appState, error) {
	log.Printf("Processing get request for %s.", key)
	url, err := createStateURL(key, statestore)
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

func getAll(states []daprState, statestore string) ([]daprState, error) {
	log.Printf("Processing get request for %d states.", len(states))

	var output = make([]daprState, 0, len(states))
	for _, state := range states {
		value, err := get(state.Key, statestore)

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

func delete(key, statestore string) error {
	log.Printf("Processing delete request for %s.", key)
	url, err := createStateURL(key, statestore)
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

func deleteAll(states []daprState, statestore string) error {
	log.Printf("Processing delete request for %d states.", len(states))

	for _, state := range states {
		err := delete(state.Key, statestore)

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

// handles all APIs for HTTP calls
func httpHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing request for %s", r.URL.RequestURI())

	// Retrieve request body contents
	var req requestResponse
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		log.Printf("Could not parse request body: %s", err.Error())
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(requestResponse{
			Message: err.Error(),
		})
		return
	}
	for i := range req.States {
		req.States[i].Metadata = map[string]string{metadataPartitionKey: partitionKey}
	}

	var res = requestResponse{}
	var uri = r.URL.RequestURI()
	var states []daprState
	var statusCode = http.StatusOK

	res.StartTime = epoch()

	cmd := mux.Vars(r)["command"]
	statestore := mux.Vars(r)["statestore"]
	switch cmd {
	case "save":
		err = save(req.States, statestore)
		if err == nil {
			// The save call to dapr side car has returned correct status.
			// Set the status code to statusNoContent
			statusCode = http.StatusNoContent
		}
	case "get":
		states, err = getAll(req.States, statestore)
		res.States = states
	case "getbulk":
		states, err = getBulk(req.States, statestore)
		res.States = states
	case "delete":
		err = deleteAll(req.States, statestore)
		statusCode = http.StatusNoContent
	case "transact":
		err = executeTransaction(req.States, statestore)
	default:
		err = fmt.Errorf("invalid URI: %s", uri)
		statusCode = http.StatusBadRequest
		res.Message = err.Error()
	}
	statusCheck := (statusCode == http.StatusOK || statusCode == http.StatusNoContent)
	if err != nil && statusCheck {
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
	// Retrieve request body contents
	var req requestResponse
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		log.Printf("Could not parse request body: %s", err.Error())
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(requestResponse{
			Message: err.Error(),
		})
		return
	}
	log.Printf("%v\n", req)
	var res requestResponse
	res.StartTime = epoch()
	var statusCode = http.StatusOK

	cmd := mux.Vars(r)["command"]
	statestore := mux.Vars(r)["statestore"]
	switch cmd {
	case "save":
		_, err := daprClient.SaveState(context.Background(), &runtimev1pb.SaveStateRequest{
			StoreName: statestore,
			States:    daprState2StateItems(req.States),
		})
		statusCode = http.StatusNoContent
		if err != nil {
			statusCode, res.Message = setErrorMessage("ExecuteSaveState", err.Error())
		}
	case "getbulk":
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
		states, err := getAllGRPC(req.States, statestore)
		if err != nil {
			statusCode, res.Message = setErrorMessage("GetState", err.Error())
		}
		res.States = states
	case "delete":
		statusCode = http.StatusNoContent
		err = deleteAllGRPC(req.States, statestore)
		if err != nil {
			statusCode, res.Message = setErrorMessage("DeleteState", err.Error())
		}
	case "transact":
		_, err = daprClient.ExecuteStateTransaction(context.Background(), &runtimev1pb.ExecuteStateTransactionRequest{
			StoreName:  statestore,
			Operations: daprState2TransactionalStateRequest(req.States),
			Metadata:   map[string]string{metadataPartitionKey: partitionKey},
		})
		if err != nil {
			statusCode, res.Message = setErrorMessage("ExecuteStateTransaction", err.Error())
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

func deleteAllGRPC(states []daprState, statestore string) error {
	for _, state := range states {
		log.Printf("deleting sate for key %s\n", state.Key)
		_, err := daprClient.DeleteState(context.Background(), &runtimev1pb.DeleteStateRequest{
			StoreName: statestore,
			Key:       state.Key,
			Metadata:  map[string]string{metadataPartitionKey: partitionKey},
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func getAllGRPC(states []daprState, statestore string) ([]daprState, error) {
	var responses []daprState
	for _, state := range states {
		log.Printf("getting state for key %s\n", state.Key)
		res, err := daprClient.GetState(context.Background(), &runtimev1pb.GetStateRequest{
			StoreName: statestore,
			Key:       state.Key,
			Metadata:  map[string]string{metadataPartitionKey: partitionKey},
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

func daprState2StateItems(daprStates []daprState) []*commonv1pb.StateItem {
	var stateItems []*commonv1pb.StateItem
	for _, daprState := range daprStates {
		val, _ := json.Marshal(daprState.Value)
		stateItems = append(stateItems, &commonv1pb.StateItem{
			Key:      daprState.Key,
			Value:    val,
			Metadata: map[string]string{metadataPartitionKey: partitionKey},
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

func createStateURL(key, statestore string) (string, error) {
	stateURL := fmt.Sprintf(stateURLTemplate, statestore)
	url, err := url.Parse(stateURL)
	if err != nil {
		return "", fmt.Errorf("could not parse %s: %s", stateURL, err.Error())
	}

	url.Path = path.Join(url.Path, key)
	url.RawQuery = "metadata.partitionKey=e2etest"

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
