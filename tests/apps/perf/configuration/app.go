/*
Copyright 2023 The Dapr Authors
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
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	daprConfigurationURL = "http://localhost:3500/v1.0/configuration/"
	daprGRPCPort         = 50001
	configStoreName      = "configstore"
	separator            = "||"
	redisHost            = "dapr-redis-master.dapr-tests.svc.cluster.local:6379"
	writeTimeout         = 5 * time.Second
)

var (
	appPort              = 3000
	updater              Updater
	httpClient           = utils.NewHTTPClient()
	grpcClient           runtimev1pb.DaprClient
	notifyCh             = make(chan struct{})
	subscribeStopChanMap = make(map[string]map[string]chan struct{})
)

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
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
	Get(key string) (string, error)
	Subscribe(keys []string) (string, error)
}

type RedisUpdater struct {
	client *redis.Client
}

func init() {
	p := os.Getenv("PORT")
	if p != "" && p != "0" {
		appPort, _ = strconv.Atoi(p)
	}
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

// extract value and version from redis value
func getRedisValueAndVersion(redisValue string) (string, string) {
	valueAndRevision := strings.Split(redisValue, separator)
	if len(valueAndRevision) == 0 {
		return "", ""
	}
	if len(valueAndRevision) == 1 {
		return valueAndRevision[0], ""
	}
	return valueAndRevision[0], valueAndRevision[1]
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

// extract key from subscribed channel
func parseRedisKeyFromChannel(eventChannel string) (string, error) {
	channelPrefix := "__keyspace@0__:"
	index := strings.Index(eventChannel, channelPrefix)
	if index == -1 {
		return "", fmt.Errorf("wrong format of event channel, it should start with '%s': eventChannel=%s", channelPrefix, eventChannel)
	}

	return eventChannel[len(channelPrefix):], nil
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

func (r *RedisUpdater) Get(key string) (string, error) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return r.client.Get(timeoutCtx, key).Result()
}

func (r *RedisUpdater) Subscribe(keys []string) (string, error) {
	subscribeID := uuid.New().String()
	keyStopChanMap := make(map[string]chan struct{})

	for _, k := range keys {
		stop := make(chan struct{})
		redisChannel := "__keyspace@0__:" + k
		keyStopChanMap[redisChannel] = stop
		go r.subscribeHandler(redisChannel, subscribeID, stop)
	}
	subscribeStopChanMap[subscribeID] = keyStopChanMap
	return subscribeID, nil
}

func (r *RedisUpdater) subscribeHandler(redisChannel string, subscribeID string, stop chan struct{}) {
	ctx := context.Background()
	r.client.ConfigSet(ctx, "notify-keyspace-events", "Kg$xe")
	p := r.client.Subscribe(ctx, redisChannel)
	defer p.Close()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case msg := <-p.Channel():
			r.handleSubscribedChange(ctx, msg, subscribeID)
		}
	}
}

func (r *RedisUpdater) handleSubscribedChange(ctx context.Context, msg *redis.Message, subscribeID string) {
	key, err := parseRedisKeyFromChannel(msg.Channel)
	if err != nil {
		log.Printf("parse redis key failed: %s", err.Error())
		return
	}
	_, err = getBaseline([]string{key})
	if err != nil {
		log.Printf("error getting values from configstore: %s", err.Error())
		return
	}
	notifyCh <- struct{}{}
}

func getDaprHTTP(keys []string) (string, error) {
	url := daprConfigurationURL + configStoreName + buildQueryParams(keys)
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("error getting key-values from config store. err: %w", err)
	}
	defer resp.Body.Close()
	respInBytes, _ := io.ReadAll(resp.Body)
	return string(respInBytes), nil
}

func getDaprGRPC(keys []string) (string, error) {
	res, err := grpcClient.GetConfiguration(context.Background(), &runtimev1pb.GetConfigurationRequest{
		StoreName: configStoreName,
		Keys:      keys,
	})
	if err != nil {
		return "", fmt.Errorf("error getting key-values from config store. err: %w", err)
	}
	items := res.GetItems()
	respInBytes, _ := json.Marshal(items)
	return string(respInBytes), nil
}

func getBaseline(keys []string) (string, error) {
	items := make(map[string]*Item, 10)
	for _, key := range keys {
		redisVal, err := updater.Get(key)
		if err != nil {
			return "", err
		}
		val, version := getRedisValueAndVersion(redisVal)
		items[key] = &Item{
			Value:    val,
			Version:  version,
			Metadata: map[string]string{},
		}
	}
	itemsInBytes, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	return string(itemsInBytes), nil
}

// getKeyValues is the handler for getting key-values from config store
func getKeyValues(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	protocol := vars["protocol"]
	test := vars["test"]
	var keys []string
	err := json.NewDecoder(r.Body).Decode(&keys)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	var response string

	if test == "baseline" {
		response, err = getBaseline(keys)
	} else if test == "dapr" {
		if protocol == "http" {
			response, err = getDaprHTTP(keys)
		} else if protocol == "grpc" {
			response, err = getDaprGRPC(keys)
		} else {
			err = fmt.Errorf("unknown protocol in Get call: %s", protocol)
		}
	}

	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, response)
}

func subscribeGRPC(keys []string) (string, error) {
	client, err := grpcClient.SubscribeConfiguration(context.Background(), &runtimev1pb.SubscribeConfigurationRequest{
		StoreName: configStoreName,
		Keys:      keys,
	})
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
			log.Print("Dapr GRPC subscribe completed")
			break
		}
		if err != nil {
			log.Printf("Error receiving config updates: %v", err)
			break
		}
		notifyCh <- struct{}{}
	}
}

func subscribeHTTP(keys []string) (string, error) {
	url := daprConfigurationURL + configStoreName + "/subscribe" + buildQueryParams(keys)
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
	if strings.Contains(string(sub), "errorCode") {
		return "", fmt.Errorf("error subscribing to config updates: %s", string(sub))
	}
	var subid map[string]interface{}
	json.Unmarshal(sub, &subid)
	log.Printf("App subscribed to config changes with subscription id: %s", subid["id"])
	subscriptionID = subid["id"].(string)
	return subscriptionID, nil
}

// subscribeHandlerHTTP is the handler for receiving updates from config store
func subscribeHandlerHTTP(w http.ResponseWriter, r *http.Request) {
	var updateEvent UpdateEvent
	err := json.NewDecoder(r.Body).Decode(&updateEvent)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	notifyCh <- struct{}{}
	sendResponse(w, http.StatusOK, "OK")
}

// startSubscription is the handler for starting a subscription to config store
func startSubscription(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	protocol := vars["protocol"]
	test := vars["test"]
	var keys []string
	err := json.NewDecoder(r.Body).Decode(&keys)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	var subscriptionID string
	if test == "baseline" {
		subscriptionID, err = updater.Subscribe(keys)
	} else {
		if protocol == "http" {
			subscriptionID, err = subscribeHTTP(keys)
		} else if protocol == "grpc" {
			subscriptionID, err = subscribeGRPC(keys)
		} else {
			err = fmt.Errorf("unknown protocol in Subscribe call: %s", protocol)
		}
	}
	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, subscriptionID)
}

func unsubscribeHTTP(subscriptionID string) (string, error) {
	url := daprConfigurationURL + configStoreName + "/" + subscriptionID + "/unsubscribe"
	resp, err := httpClient.Get(url)
	if err != nil {
		return "", fmt.Errorf("error unsubscribing config updates: %w", err)
	}
	defer resp.Body.Close()
	respInBytes, _ := io.ReadAll(resp.Body)
	return string(respInBytes), nil
}

func unsubscribeGRPC(subscriptionID string) (string, error) {
	resp, err := grpcClient.UnsubscribeConfiguration(context.Background(), &runtimev1pb.UnsubscribeConfigurationRequest{
		StoreName: configStoreName,
		Id:        subscriptionID,
	})
	if err != nil {
		return "", fmt.Errorf("error unsubscribing config updates: %w", err)
	}
	if !resp.GetOk() {
		return "", fmt.Errorf("error unsubscribing config updates: %s", resp.GetMessage())
	}
	return resp.GetMessage(), nil
}

func unsubscribeBaseline(subscriptionID string) (string, error) {
	if keyStopChanMap, ok := subscribeStopChanMap[subscriptionID]; ok {
		for _, stop := range keyStopChanMap {
			close(stop)
		}
		delete(subscribeStopChanMap, subscriptionID)
		return "SUCESSS", nil
	}
	return "", fmt.Errorf("subscription with id %s does not exist", subscriptionID)
}

// stopSubscription is the handler for unsubscribing from config store
func stopSubscription(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	subscriptionID := vars["subscriptionID"]
	protocol := vars["protocol"]
	test := vars["test"]
	var response string
	var err error
	if test == "baseline" {
		response, err = unsubscribeBaseline(subscriptionID)
	} else {
		if protocol == "http" {
			response, err = unsubscribeHTTP(subscriptionID)
		} else if protocol == "grpc" {
			response, err = unsubscribeGRPC(subscriptionID)
		} else {
			err = fmt.Errorf("unknown protocol in unsubscribe call: %s", protocol)
		}
	}

	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, response)
}

// initializeUpdater is the handler for initializing config updater
func initializeUpdater(w http.ResponseWriter, r *http.Request) {
	log.Printf("Initializing updater with redis component")
	updater = &RedisUpdater{}
	err := updater.Init()
	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, "OK")
}

// updateKeyValues is the handler for updating key values in the config store
func updateKeyValues(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	wait := vars["wait"]
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
	if wait == "true" {
		<-notifyCh
	}
	sendResponse(w, http.StatusOK, "OK")
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/initialize-updater", initializeUpdater).Methods("GET")
	router.HandleFunc("/update/{wait}", updateKeyValues).Methods("POST")
	router.HandleFunc("/get/{test}/{protocol}", getKeyValues).Methods("POST")

	router.HandleFunc("/subscribe/{test}/{protocol}", startSubscription).Methods("POST")
	router.HandleFunc("/unsubscribe/{test}/{protocol}/{subscriptionID}", stopSubscription).Methods("GET")
	router.HandleFunc("/configuration/{storeName}/{key}", subscribeHandlerHTTP).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))
	return router
}

func main() {
	grpcClient = utils.GetGRPCClient(daprGRPCPort)
	log.Printf("Starting application on  http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
