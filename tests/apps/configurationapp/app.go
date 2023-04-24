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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	daprGRPCPort               = 50001
	configStore                = "configstore"
	daprConfigurationURLStable = "http://localhost:3500/v1.0/configuration/"
	daprConfigurationURLAlpha1 = "http://localhost:3500/v1.0-alpha1/configuration/"
	separator                  = "||"
	redisHost                  = "dapr-redis-master.dapr-tests.svc.cluster.local:6379"
	writeTimeout               = 5 * time.Second
	alpha1Endpoint             = "alpha1"
)

var (
	appPort         = 3000
	lock            sync.Mutex
	receivedUpdates map[string][]string
	updater         Updater
	httpClient      = utils.NewHTTPClient()
	grpcClient      runtimev1pb.DaprClient
)

type UnsubscribeConfigurationResponse struct {
	Ok      bool   `json:"ok,omitempty"`
	Message string `json:"message,omitempty"`
}

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type receivedMessagesResponse struct {
	ReceivedUpdates []string `json:"received-messages"`
}

type Item struct {
	Value    string            `json:"value,omitempty"`
	Version  string            `json:"version,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type UpdateEvent struct {
	ID    string           `json:"id"`
	Items map[string]*Item `json:"items"`
}

type Updater interface {
	Init() error
	Update(items map[string]*Item) error
}

type RedisUpdater struct {
	client *redis.Client
}

func (r *RedisUpdater) Init() error {
	opts := &redis.Options{
		Addr:     redisHost,
		Password: "",
		DB:       0,
	}
	r.client = redis.NewClient(opts)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	err := r.client.Ping(timeoutCtx).Err()
	if err != nil {
		return fmt.Errorf("error connecting to redis config store. err: %s", err)
	}
	return nil
}

func (r *RedisUpdater) Update(items map[string]*Item) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	values := getRedisValuesFromItems(items)
	valuesWithCommand := append([]interface{}{"MSET"}, values...)
	return r.client.Do(timeoutCtx, valuesWithCommand...).Err()
}

func init() {
	p := os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
	receivedUpdates = make(map[string][]string)
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(appResponse{Message: "OK"})
}

// sendResponse returns response with status code and message
func sendResponse(w http.ResponseWriter, statusCode int, message string) {
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(appResponse{Message: message})
}

// return key-value pairs as a list of strings
func getRedisValuesFromItems(items map[string]*Item) []interface{} {
	m := make([]interface{}, 0, 2*len(items)+1)
	for key, item := range items {
		val := item.Value + separator + item.Version
		m = append(m, key, val)
	}
	return m
}

func getHTTP(keys []string, endpointType string) (string, error) {
	daprConfigurationURL := daprConfigurationURLStable
	if endpointType == alpha1Endpoint {
		daprConfigurationURL = daprConfigurationURLAlpha1
	}
	url := daprConfigurationURL + configStore + buildQueryParams(keys)
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("error getting key-values from config store. err: %w", err)
	}
	defer resp.Body.Close()
	respInBytes, _ := io.ReadAll(resp.Body)
	return string(respInBytes), nil
}

func getGRPC(keys []string, endpointType string) (string, error) {
	getConfigGRPC := grpcClient.GetConfiguration
	if endpointType == alpha1Endpoint {
		getConfigGRPC = grpcClient.GetConfigurationAlpha1
	}
	res, err := getConfigGRPC(context.Background(), &runtimev1pb.GetConfigurationRequest{
		StoreName: configStore,
		Keys:      keys,
	})
	if err != nil {
		return "", fmt.Errorf("error getting key-values from config store. err: %w", err)
	}
	items := res.GetItems()
	respInBytes, _ := json.Marshal(items)
	return string(respInBytes), nil
}

// getKeyValues is the handler for getting key-values from config store
func getKeyValues(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	protocol := vars["protocol"]
	endpointType := vars["endpointType"]
	var keys []string
	err := json.NewDecoder(r.Body).Decode(&keys)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	var response string
	if protocol == "http" {
		response, err = getHTTP(keys, endpointType)
	} else if protocol == "grpc" {
		response, err = getGRPC(keys, endpointType)
	} else {
		err = fmt.Errorf("unknown protocol in Get call: %s", protocol)
	}
	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, response)
}

func buildQueryParams(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	ret := "?key=" + keys[0]
	for i := 1; i < len(keys); i++ {
		ret = ret + "&key=" + keys[i]
	}
	return ret
}

func subscribeGRPC(keys []string, endpointType string) (string, error) {
	var client runtimev1pb.Dapr_SubscribeConfigurationClient
	var err error
	if endpointType == alpha1Endpoint {
		client, err = grpcClient.SubscribeConfigurationAlpha1(context.Background(), &runtimev1pb.SubscribeConfigurationRequest{
			StoreName: configStore,
			Keys:      keys,
		})
	} else {
		client, err = grpcClient.SubscribeConfiguration(context.Background(), &runtimev1pb.SubscribeConfigurationRequest{
			StoreName: configStore,
			Keys:      keys,
		})
	}
	if err != nil {
		return "", fmt.Errorf("error subscribing config updates: %w", err)
	}
	res, err := client.Recv()
	if errors.Is(err, io.EOF) {
		return "", fmt.Errorf("error subscribe: stream closed before receiving ID")
	}
	if err != nil {
		return "", fmt.Errorf("error subscribe: %w", err)
	}
	subscriptionID := res.GetId()
	go subscribeHandlerGRPC(client)
	log.Printf("App subscribed to config changes with subscription id: %s", subscriptionID)
	return subscriptionID, nil
}

func subscribeHandlerGRPC(client runtimev1pb.Dapr_SubscribeConfigurationClient) {
	for {
		rsp, err := client.Recv()
		if errors.Is(err, io.EOF) || rsp == nil {
			log.Print("Dapr subscribe finished")
			break
		}
		if err != nil {
			log.Printf("Error receiving config updates: %v", err)
			break
		}
		subscribeID := rsp.GetId()
		configurationItems := make(map[string]*Item)
		for key, item := range rsp.GetItems() {
			configurationItems[key] = &Item{
				Value:   item.Value,
				Version: item.Version,
			}
		}
		receivedItemsInBytes, _ := json.Marshal(configurationItems)
		receivedItems := string(receivedItemsInBytes)
		log.Printf("SubscriptionID: %s, Received item: %s", subscribeID, receivedItems)
		lock.Lock()
		if receivedUpdates[subscribeID] == nil {
			receivedUpdates[subscribeID] = make([]string, 10)
		}
		receivedUpdates[subscribeID] = append(receivedUpdates[subscribeID], receivedItems)
		lock.Unlock()
	}
}

func subscribeHTTP(keys []string, endpointType string) (string, error) {
	daprConfigurationURL := daprConfigurationURLStable
	if endpointType == alpha1Endpoint {
		daprConfigurationURL = daprConfigurationURLAlpha1
	}
	url := daprConfigurationURL + configStore + "/subscribe" + buildQueryParams(keys)
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("error subscribing config updates: %w", err)
	}
	defer resp.Body.Close()
	sub, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading subscription Id: %w", err)
	}
	var subscriptionID string
	if !strings.Contains(string(sub), "errorCode") {
		var subid map[string]interface{}
		json.Unmarshal(sub, &subid)
		log.Printf("App subscribed to config changes with subscription id: %s", subid["id"])
		subscriptionID = subid["id"].(string)
	} else {
		return "", fmt.Errorf("error subscribing to config updates: %s", string(sub))
	}
	return subscriptionID, nil
}

// startSubscription is the handler for starting a subscription to config store
func startSubscription(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	protocol := vars["protocol"]
	endpointType := vars["endpointType"]
	var keys []string
	err := json.NewDecoder(r.Body).Decode(&keys)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	var subscriptionID string
	if protocol == "http" {
		subscriptionID, err = subscribeHTTP(keys, endpointType)
	} else if protocol == "grpc" {
		subscriptionID, err = subscribeGRPC(keys, endpointType)
	} else {
		err = fmt.Errorf("unknown protocol in Subscribe call: %s", protocol)
	}

	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, subscriptionID)
}

func unsubscribeHTTP(subscriptionID string, endpointType string) (string, error) {
	daprConfigurationURL := daprConfigurationURLStable
	if endpointType == alpha1Endpoint {
		daprConfigurationURL = daprConfigurationURLAlpha1
	}
	url := daprConfigurationURL + configStore + "/" + subscriptionID + "/unsubscribe"
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("error unsubscribing config updates: %w", err)
	}
	defer resp.Body.Close()
	respInBytes, _ := io.ReadAll(resp.Body)
	var unsubscribeResp UnsubscribeConfigurationResponse
	err = json.Unmarshal(respInBytes, &unsubscribeResp)
	if err != nil {
		return "", fmt.Errorf("error unmarshalling unsubscribe response: %s", err.Error())
	}
	if !unsubscribeResp.Ok {
		return "", fmt.Errorf("error subscriptionID not found: %s", unsubscribeResp.Message)
	}
	return string(respInBytes), nil
}

func unsubscribeGRPC(subscriptionID string, endpointType string) (string, error) {
	unsubscribeConfigGRPC := grpcClient.UnsubscribeConfiguration
	if endpointType == alpha1Endpoint {
		unsubscribeConfigGRPC = grpcClient.UnsubscribeConfigurationAlpha1
	}
	resp, err := unsubscribeConfigGRPC(context.Background(), &runtimev1pb.UnsubscribeConfigurationRequest{
		StoreName: configStore,
		Id:        subscriptionID,
	})
	if err != nil {
		return "", fmt.Errorf("error unsubscribing config updates: %w", err)
	}
	if !resp.Ok {
		return "", fmt.Errorf("error subscriptionID not found: %s", resp.GetMessage())
	}
	return resp.GetMessage(), nil
}

// stopSubscription is the handler for unsubscribing from config store
func stopSubscription(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	subscriptionID := vars["subscriptionID"]
	protocol := vars["protocol"]
	endpointType := vars["endpointType"]
	var response string
	var err error
	if protocol == "http" {
		response, err = unsubscribeHTTP(subscriptionID, endpointType)
	} else if protocol == "grpc" {
		response, err = unsubscribeGRPC(subscriptionID, endpointType)
	} else {
		err = fmt.Errorf("unknown protocol in unsubscribe call: %s", protocol)
	}
	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, response)
}

// configurationUpdateHandler is the handler for receiving updates from config store
func configurationUpdateHandler(w http.ResponseWriter, r *http.Request) {
	var updateEvent UpdateEvent
	err := json.NewDecoder(r.Body).Decode(&updateEvent)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	subID := updateEvent.ID
	receivedItemsInBytes, _ := json.Marshal(updateEvent.Items)
	receivedItems := string(receivedItemsInBytes)

	log.Printf("SubscriptionID: %s, Received item: %s", subID, receivedItems)
	lock.Lock()
	defer lock.Unlock()
	if receivedUpdates[subID] == nil {
		receivedUpdates[subID] = make([]string, 10)
	}
	receivedUpdates[subID] = append(receivedUpdates[subID], receivedItems)
	sendResponse(w, http.StatusOK, "OK")
}

// getReceivedUpdates is the handler for getting received updates from config store
func getReceivedUpdates(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	subID := vars["subscriptionID"]

	lock.Lock()
	defer lock.Unlock()
	response := receivedMessagesResponse{
		ReceivedUpdates: receivedUpdates[subID],
	}
	receivedUpdates[subID] = make([]string, 10)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// initializeUpdater is the handler for initializing config updater
func initializeUpdater(w http.ResponseWriter, r *http.Request) {
	var component string
	err := json.NewDecoder(r.Body).Decode(&component)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	log.Printf("Initializing updater with component: %s\n", component)
	switch component {
	case "redis":
		updater = &RedisUpdater{}
		err := updater.Init()
		if err != nil {
			sendResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
	default:
		sendResponse(w, http.StatusBadRequest, "Invalid component:"+component+".Allowed values are 'redis'")
		return
	}
	sendResponse(w, http.StatusOK, "OK")
}

// updateKeyValues is the handler for updating key values in the config store
func updateKeyValues(w http.ResponseWriter, r *http.Request) {
	items := make(map[string]*Item, 10)
	err := json.NewDecoder(r.Body).Decode(&items)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	err = updater.Update(items)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, "OK")
}

// appRouter initializes restful api router
func appRouter() *mux.Router {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/initialize-updater", initializeUpdater).Methods("POST")
	router.HandleFunc("/get-key-values/{protocol}/{endpointType}", getKeyValues).Methods("POST")
	router.HandleFunc("/subscribe/{protocol}/{endpointType}", startSubscription).Methods("POST")
	router.HandleFunc("/unsubscribe/{subscriptionID}/{protocol}/{endpointType}", stopSubscription).Methods("GET")
	router.HandleFunc("/configuration/{storeName}/{key}", configurationUpdateHandler).Methods("POST")
	router.HandleFunc("/get-received-updates/{subscriptionID}", getReceivedUpdates).Methods("GET")
	router.HandleFunc("/update-key-values", updateKeyValues).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))
	return router
}

func main() {
	grpcClient = utils.GetGRPCClient(daprGRPCPort)
	log.Printf("Starting application on  http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
