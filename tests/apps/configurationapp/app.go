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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	daprGRPCPort               = 50001
	daprConfigurationURLStable = "http://localhost:3500/v1.0/configuration/"
	daprConfigurationURLAlpha1 = "http://localhost:3500/v1.0-alpha1/configuration/"
	separator                  = "||"
	redisHost                  = "dapr-redis-master.dapr-tests.svc.cluster.local:6379"
	writeTimeout               = 5 * time.Second
	alpha1Endpoint             = "alpha1"
	postgresComponent          = "postgres"
	redisComponent             = "redis"
	postgresChannel            = "config"
	postgresConnectionString   = "host=dapr-postgres-postgresql.dapr-tests.svc.cluster.local user=postgres password=example port=5432 connect_timeout=10 database=dapr_test"
	postgresConfigTable        = "configtable"
)

var (
	appPort         = 3000
	lock            sync.Mutex
	receivedUpdates map[string][]string
	updater         Updater
	httpClient      = utils.NewHTTPClient()
	grpcClient      runtimev1pb.DaprClient
	grpcSubs        = make(map[string]context.CancelFunc)
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
	AddKey(items map[string]*Item) error
	UpdateKey(items map[string]*Item) error
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

func (r *RedisUpdater) AddKey(items map[string]*Item) error {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	values := getRedisValuesFromItems(items)
	valuesWithCommand := append([]interface{}{"MSET"}, values...)
	return r.client.Do(timeoutCtx, valuesWithCommand...).Err()
}

func (r *RedisUpdater) UpdateKey(items map[string]*Item) error {
	return r.AddKey(items)
}

type PostgresUpdater struct {
	client *pgxpool.Pool
}

func createAndSetTable(ctx context.Context, client *pgxpool.Pool, configTable string) error {
	// Creating table if not exists
	_, err := client.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+configTable+" (KEY VARCHAR NOT NULL, VALUE VARCHAR NOT NULL, VERSION VARCHAR NOT NULL, METADATA JSON)")
	if err != nil {
		return fmt.Errorf("error creating table : %w", err)
	}

	// Deleting existing data
	_, err = client.Exec(ctx, "TRUNCATE TABLE "+configTable)
	if err != nil {
		return fmt.Errorf("error truncating table : %w", err)
	}

	return nil
}

func (r *PostgresUpdater) CreateTrigger(channel string) error {
	ctx := context.Background()
	procedureName := "configuration_event_" + channel
	createConfigEventSQL := `CREATE OR REPLACE FUNCTION ` + procedureName + `() RETURNS TRIGGER AS $$
		DECLARE
			data json;
			notification json;

		BEGIN

			IF (TG_OP = 'DELETE') THEN
				data = row_to_json(OLD);
			ELSE
				data = row_to_json(NEW);
			END IF;

			notification = json_build_object(
							'table',TG_TABLE_NAME,
							'action', TG_OP,
							'data', data);

			PERFORM pg_notify('` + channel + `' ,notification::text);
			RETURN NULL;
		END;
	$$ LANGUAGE plpgsql;
	`
	_, err := r.client.Exec(ctx, createConfigEventSQL)
	if err != nil {
		return fmt.Errorf("error creating config event function : %w", err)
	}
	triggerName := "configTrigger_" + channel
	createTriggerSQL := "CREATE TRIGGER " + triggerName + " AFTER INSERT OR UPDATE OR DELETE ON " + postgresConfigTable + " FOR EACH ROW EXECUTE PROCEDURE " + procedureName + "();"
	_, err = r.client.Exec(ctx, createTriggerSQL)
	if err != nil {
		return fmt.Errorf("error creating config trigger : %w", err)
	}
	return nil
}

func (r *PostgresUpdater) Init() error {
	ctx := context.Background()
	config, err := pgxpool.ParseConfig(postgresConnectionString)
	if err != nil {
		return fmt.Errorf("postgres configuration store connection error : %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("postgres configuration store connection error : %w", err)
	}
	err = pool.Ping(ctx)
	if err != nil {
		return fmt.Errorf("postgres configuration store ping error : %w", err)
	}
	r.client = pool
	err = createAndSetTable(ctx, r.client, postgresConfigTable)
	if err != nil {
		return fmt.Errorf("postgres configuration store table creation error : %w", err)
	}
	err = r.CreateTrigger(postgresChannel)
	if err != nil {
		return fmt.Errorf("postgres configuration store trigger creation error : %w", err)
	}
	return nil
}

func buildAddQuery(items map[string]*Item, configTable string) (string, []interface{}, error) {
	query := ""
	paramWildcard := make([]string, 0, len(items))
	params := make([]interface{}, 0, 4*len(items))
	if len(items) == 0 {
		return query, params, fmt.Errorf("empty list of items")
	}
	var queryBuilder strings.Builder
	queryBuilder.WriteString("INSERT INTO " + configTable + " (KEY, VALUE, VERSION, METADATA) VALUES ")

	paramPosition := 1
	for key, item := range items {
		paramWildcard = append(paramWildcard, "($"+strconv.Itoa(paramPosition)+", $"+strconv.Itoa(paramPosition+1)+", $"+strconv.Itoa(paramPosition+2)+", $"+strconv.Itoa(paramPosition+3)+")")
		params = append(params, key, item.Value, item.Version, item.Metadata)
		paramPosition += 4
	}
	queryBuilder.WriteString(strings.Join(paramWildcard, " , "))
	query = queryBuilder.String()
	return query, params, nil
}

func (r *PostgresUpdater) AddKey(items map[string]*Item) error {
	query, params, err := buildAddQuery(items, postgresConfigTable)
	if err != nil {
		return err
	}
	_, err = r.client.Exec(context.Background(), query, params...)
	if err != nil {
		return err
	}
	return nil
}

func (r *PostgresUpdater) UpdateKey(items map[string]*Item) error {
	if len(items) == 0 {
		return fmt.Errorf("empty list of items")
	}
	for key, item := range items {
		var params []interface{}
		query := "UPDATE " + postgresConfigTable + " SET VALUE = $1, VERSION = $2, METADATA = $3 WHERE KEY = $4"
		params = append(params, item.Value, item.Version, item.Metadata, key)
		_, err := r.client.Exec(context.Background(), query, params...)
		if err != nil {
			return err
		}
	}
	return nil
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

func getHTTP(keys []string, endpointType string, configStore string) (string, error) {
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

func getGRPC(keys []string, endpointType string, configStore string) (string, error) {
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
	configStore := vars["configStore"]
	var keys []string
	err := json.NewDecoder(r.Body).Decode(&keys)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	var response string
	if protocol == "http" {
		response, err = getHTTP(keys, endpointType, configStore)
	} else if protocol == "grpc" {
		response, err = getGRPC(keys, endpointType, configStore)
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

func subscribeGRPC(keys []string, endpointType string, configStore string, component string) (subscriptionID string, err error) {
	var client runtimev1pb.Dapr_SubscribeConfigurationClient
	subscribeConfigurationRequest := &runtimev1pb.SubscribeConfigurationRequest{
		StoreName: configStore,
		Keys:      keys,
	}
	if component == postgresComponent {
		subscribeConfigurationRequest.Metadata = map[string]string{
			"pgNotifyChannel": postgresChannel,
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	if endpointType == alpha1Endpoint {
		client, err = grpcClient.SubscribeConfigurationAlpha1(ctx, subscribeConfigurationRequest)
	} else {
		client, err = grpcClient.SubscribeConfiguration(ctx, subscribeConfigurationRequest)
	}
	if err != nil {
		cancel()
		return "", fmt.Errorf("error subscribing config updates: %w", err)
	}
	res, err := client.Recv()
	if errors.Is(err, io.EOF) {
		cancel()
		return "", fmt.Errorf("error subscribe: stream closed before receiving ID")
	}
	if err != nil {
		cancel()
		return "", fmt.Errorf("error subscribe: %w", err)
	}
	subscriptionID = res.GetId()
	go subscribeHandlerGRPC(client)
	log.Printf("App subscribed to config changes with subscription id: %s", subscriptionID)

	lock.Lock()
	grpcSubs[subscriptionID] = cancel
	lock.Unlock()

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
				Value:   item.GetValue(),
				Version: item.GetVersion(),
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

func subscribeHTTP(keys []string, endpointType string, configStore string, component string) (string, error) {
	daprConfigurationURL := daprConfigurationURLStable
	if endpointType == alpha1Endpoint {
		daprConfigurationURL = daprConfigurationURLAlpha1
	}
	url := daprConfigurationURL + configStore + "/subscribe" + buildQueryParams(keys)
	if component == postgresComponent {
		url = url + "&metadata.pgNotifyChannel=" + postgresChannel
	}
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
	configStore := vars["configStore"]
	component := vars["component"]
	var keys []string
	err := json.NewDecoder(r.Body).Decode(&keys)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	var subscriptionID string
	if protocol == "http" {
		subscriptionID, err = subscribeHTTP(keys, endpointType, configStore, component)
	} else if protocol == "grpc" {
		subscriptionID, err = subscribeGRPC(keys, endpointType, configStore, component)
	} else {
		err = fmt.Errorf("unknown protocol in Subscribe call: %s", protocol)
	}

	if err != nil {
		sendResponse(w, http.StatusInternalServerError, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, subscriptionID)
}

func unsubscribeHTTP(subscriptionID string, endpointType string, configStore string) (string, error) {
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

func unsubscribeGRPC(subscriptionID string) error {
	lock.Lock()
	cancel := grpcSubs[subscriptionID]
	delete(grpcSubs, subscriptionID)
	lock.Unlock()

	if cancel == nil {
		return errors.New("error subscriptionID not found")
	}
	cancel()

	return nil
}

// stopSubscription is the handler for unsubscribing from config store
func stopSubscription(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	subscriptionID := vars["subscriptionID"]
	protocol := vars["protocol"]
	endpointType := vars["endpointType"]
	configStore := vars["configStore"]
	response := "OK"
	var err error
	if protocol == "http" {
		response, err = unsubscribeHTTP(subscriptionID, endpointType, configStore)
	} else if protocol == "grpc" {
		err = unsubscribeGRPC(subscriptionID)
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
	case redisComponent:
		updater = &RedisUpdater{}
		err := updater.Init()
		if err != nil {
			sendResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
	case postgresComponent:
		updater = &PostgresUpdater{}
		err := updater.Init()
		if err != nil {
			sendResponse(w, http.StatusInternalServerError, err.Error())
			return
		}
	default:
		sendResponse(w, http.StatusBadRequest, "Invalid component:"+component+".Allowed values are 'redis' and 'postgres'")
		return
	}
	sendResponse(w, http.StatusOK, "OK")
}

// updateKeyValues is the handler for updating key values in the config store
func updateKeyValues(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	operation := vars["operation"]
	items := make(map[string]*Item, 10)
	err := json.NewDecoder(r.Body).Decode(&items)
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	switch operation {
	case "add":
		err = updater.AddKey(items)
	case "update":
		err = updater.UpdateKey(items)
	default:
		err = fmt.Errorf("unknown operation: %s", operation)
	}
	if err != nil {
		sendResponse(w, http.StatusBadRequest, err.Error())
		return
	}
	sendResponse(w, http.StatusOK, "OK")
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := mux.NewRouter().StrictSlash(true)

	// Log requests and their processing time
	router.Use(utils.LoggerMiddleware)

	router.HandleFunc("/", indexHandler).Methods("GET")
	router.HandleFunc("/initialize-updater", initializeUpdater).Methods("POST")
	router.HandleFunc("/get-key-values/{protocol}/{endpointType}/{configStore}", getKeyValues).Methods("POST")
	router.HandleFunc("/subscribe/{protocol}/{endpointType}/{configStore}/{component}", startSubscription).Methods("POST")
	router.HandleFunc("/unsubscribe/{subscriptionID}/{protocol}/{endpointType}/{configStore}", stopSubscription).Methods("GET")
	router.HandleFunc("/configuration/{storeName}/{key}", configurationUpdateHandler).Methods("POST")
	router.HandleFunc("/get-received-updates/{subscriptionID}", getReceivedUpdates).Methods("GET")
	router.HandleFunc("/update-key-values/{operation}", updateKeyValues).Methods("POST")

	router.Use(mux.CORSMethodMiddleware(router))
	return router
}

func main() {
	grpcClient = utils.GetGRPCClient(daprGRPCPort)
	log.Printf("Starting application on  http://localhost:%d", appPort)
	utils.StartServer(appPort, appRouter, true, false)
}
