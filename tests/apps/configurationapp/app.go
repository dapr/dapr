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

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	daprHost        = "localhost"
	daprHTTPPort    = "3500"
	configStoreName = "configstore"
	separator       = "||"
	redisHost       = "dapr-redis-master.dapr-tests.svc.cluster.local:6379"
	writeTimeout    = 5 * time.Second
)

var (
	appPort         = 3000
	lock            sync.Mutex
	receivedUpdates map[string][]string
	updater         Updater
	httpClient      = utils.NewHTTPClient()
)

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

// getKeyValues is the handler for getting key-values from config store
func getKeyValues(w http.ResponseWriter, r *http.Request) {
	var keys []string
	err := json.NewDecoder(r.Body).Decode(&keys)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	url := "http://" + daprHost + ":" + daprHTTPPort + "/v1.0-alpha1/configuration/" + configStoreName + buildQueryParams(keys)
	resp, err := httpClient.Get(url)
	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer resp.Body.Close()
	respInBytes, _ := io.ReadAll(resp.Body)
	sendResponse(w, http.StatusOK, string(respInBytes))
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

func subscribeToConfigUpdates(keys []string) (string, error) {
	url := "http://" + daprHost + ":" + daprHTTPPort + "/v1.0-alpha1/configuration/" + configStoreName + "/subscribe" + buildQueryParams(keys)
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("error subscribing config updates: %s", err)
	}
	defer resp.Body.Close()
	sub, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading subscription Id: %s", err)
	}
	var subscriptionID string
	if !strings.Contains(string(sub), "errorCode") {
		var subid map[string]interface{}
		json.Unmarshal(sub, &subid)
		log.Printf("App subscribed to config changes with subscription id: %s\n", subid["id"])
		subscriptionID = subid["id"].(string)
	} else {
		return "", fmt.Errorf("error subscribing to config updates: %s", string(sub))
	}
	return subscriptionID, nil
}

// startSubscription is the handler for starting a subscription to config store
func startSubscription(w http.ResponseWriter, r *http.Request) {
	var keys []string
	err := json.NewDecoder(r.Body).Decode(&keys)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	subscriptionID, err := subscribeToConfigUpdates(keys)
	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, subscriptionID)
}

// stopSubscription is the handler for unsubscribing from config store
func stopSubscription(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	subscriptionID := vars["subscriptionID"]
	url := "http://" + daprHost + ":" + daprHTTPPort + "/v1.0-alpha1/configuration/" + configStoreName + "/" + subscriptionID + "/unsubscribe"
	resp, err := httpClient.Get(url)
	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer resp.Body.Close()
	respInBytes, _ := io.ReadAll(resp.Body)
	sendResponse(w, http.StatusOK, string(respInBytes))
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

	log.Printf("SubscriptionID: %s, Received item: %s\n", subID, receivedItems)
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
	router.HandleFunc("/get-key-values", getKeyValues).Methods("POST")
	router.HandleFunc("/subscribe", startSubscription).Methods("POST")
	router.HandleFunc("/unsubscribe/{subscriptionID}", stopSubscription).Methods("GET")
	router.HandleFunc("/configuration/{storeName}/{key}", configurationUpdateHandler).Methods("POST")
	router.HandleFunc("/get-received-updates/{subscriptionID}", getReceivedUpdates).Methods("GET")
	router.HandleFunc("/update-key-values", updateKeyValues).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))
	return router
}

func main() {
	log.Printf("Starting application on  http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
