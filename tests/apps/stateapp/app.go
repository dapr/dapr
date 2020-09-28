// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
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
	stateURL            = "http://localhost:3500/v1.0/state/statestore"
	bulkStateURL        = "http://localhost:3500/v1.0/state/statestore/bulk"
	stateTransactionURL = "http://localhost:3500/v1.0/state/statestore/transaction"
)

// appState represents a state in this app.
type appState struct {
	Data string `json:"data,omitempty"`
}

// daprState represents a state in Dapr.
type daprState struct {
	Key           string    `json:"key,omitempty"`
	Value         *appState `json:"value,omitempty"`
	OperationType string    `json:"operationType,omitempty"`
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

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
}

func save(states []daprState) error {
	log.Printf("Processing save request for %d entries.", len(states))

	jsonValue, err := json.Marshal(states)
	if err != nil {
		log.Printf("Could save states in Dapr: %s", err.Error())
		return err
	}

	log.Printf("Posting state to %s with '%s'", stateURL, jsonValue)
	res, err := http.Post(stateURL, "application/json", bytes.NewBuffer(jsonValue))
	if err != nil {
		return err
	}

	defer res.Body.Close()
	return nil
}

func get(key string) (*appState, error) {
	log.Printf("Processing get request for %s.", key)
	url, err := createStateURL(key)
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
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("could not load value for key %s from Dapr: %s", key, err.Error())
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("fail to get value for key %s from Dapr: %s", key, body)
	}

	log.Printf("Found state for key %s: %s", key, body)

	var state = new(appState)
	if len(body) == 0 {
		return nil, nil
	}

	// a key not found in Dapr will return 200 but an empty response.
	err = json.Unmarshal(body, &state)
	if err != nil {
		var stateData string
		stringMarshalErr := json.Unmarshal(body, &stateData)
		if stringMarshalErr != nil {
			return nil, fmt.Errorf("could not parse value for key %s from Dapr: %s", key, err.Error())
		}
		state.Data = stateData
	}

	return state, nil
}

func getAll(states []daprState) ([]daprState, error) {
	log.Printf("Processing get request for %d states.", len(states))

	var output = make([]daprState, 0, len(states))
	for _, state := range states {
		value, err := get(state.Key)

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

func getBulk(states []daprState) ([]daprState, error) {
	log.Printf("Processing get bulk request for %d states.", len(states))

	var output = make([]daprState, 0, len(states))

	url, err := createBulkStateURL()
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

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("could not load values for bulk get from Dapr: %s", err.Error())
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

func delete(key string) error {
	log.Printf("Processing delete request for %s.", key)
	url, err := createStateURL(key)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("could not create delete request for key %s in Dapr: %s", key, err.Error())
	}

	log.Printf("Deleting state for %s", url)
	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not delete key %s in Dapr: %s", key, err.Error())
	}

	defer res.Body.Close()
	return nil
}

func deleteAll(states []daprState) error {
	log.Printf("Processing delete request for %d states.", len(states))

	for _, state := range states {
		err := delete(state.Key)

		if err != nil {
			return err
		}
	}

	return nil
}

func executeTransaction(states []daprState) error {
	var transactionalOperations []map[string]interface{}

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

	var res = requestResponse{}
	var uri = r.URL.RequestURI()
	var states []daprState
	var statusCode = http.StatusOK

	res.StartTime = epoch()

	cmd := mux.Vars(r)["command"]
	switch cmd {
	case "save":
		err = save(req.States)
	case "get":
		states, err = getAll(req.States)
		res.States = states
	case "getbulk":
		states, err = getBulk(req.States)
		res.States = states
	case "delete":
		err = deleteAll(req.States)
	case "transact":
		err = executeTransaction(req.States)
	default:
		err = fmt.Errorf("invalid URI: %s", uri)
		statusCode = http.StatusBadRequest
		res.Message = err.Error()
	}

	if err != nil && statusCode == http.StatusOK {
		statusCode = http.StatusInternalServerError
		res.Message = err.Error()
	}

	res.EndTime = epoch()

	if statusCode != http.StatusOK {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
}

// Handles State TransasctionRequest for GRPC
func grpcHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("Processing request for ", r.URL.RequestURI())
	log.Println(fmt.Sprintf("%s", r.Body))
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

	var res requestResponse
	res.StartTime = epoch()
	var statusCode = http.StatusOK

	daprPort, _ := os.LookupEnv("DAPR_GRPC_PORT")
	daprAddress := fmt.Sprintf("127.0.0.1:%s", daprPort)
	log.Printf("dapr grpc address is %s\n", daprAddress)
	conn, err := grpc.Dial(daprAddress, grpc.WithInsecure())

	if err != nil {
		log.Printf(err.Error())
	}
	defer conn.Close()

	client := runtimev1pb.NewDaprClient(conn)
	cmd := mux.Vars(r)["command"]
	switch cmd {
	case "transact":
		_, err = client.ExecuteStateTransaction(context.Background(), &runtimev1pb.ExecuteStateTransactionRequest{
			StoreName:  "statestore",
			Operations: daprState2TransactionalStateRequest(req.States),
		})
		if err != nil {
			statusCode = http.StatusInternalServerError
			log.Printf("GRPC Execute State Transaction had error %s\n", err.Error())
			res.Message = err.Error()
		}
	default:
		statusCode = http.StatusInternalServerError
		unsupportedCommandMessage := fmt.Sprintf("GRPC protocol command %s not supported", cmd)
		log.Printf(unsupportedCommandMessage)
		res.Message = unsupportedCommandMessage
	}

	res.EndTime = epoch()
	if statusCode != http.StatusOK {
		log.Printf("Error status code %v: %v", statusCode, res.Message)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(res)
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

func createStateURL(key string) (string, error) {
	url, err := url.Parse(stateURL)
	if err != nil {
		return "", fmt.Errorf("could not parse %s: %s", stateURL, err.Error())
	}

	url.Path = path.Join(url.Path, key)
	return url.String(), nil
}

func createBulkStateURL() (string, error) {
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
	router.HandleFunc("/test/http/{command}", httpHandler).Methods("POST")
	router.HandleFunc("/test/grpc/{command}", grpcHandler).Methods("POST")
	router.Use(mux.CORSMethodMiddleware(router))

	return router
}

func main() {
	log.Printf("State App - listening on http://localhost:%d", appPort)
	log.Printf("State endpoint - to be saved at %s", stateURL)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
