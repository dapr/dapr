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
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	appPort                  = 3000
	daprV1URL                = "http://localhost:3500/v1.0"
	actorMethodURLFormat     = daprV1URL + "/actors/%s/%s/%s/%s"
	actorSaveStateURLFormat  = daprV1URL + "/actors/%s/%s/state/"
	actorGetStateURLFormat   = daprV1URL + "/actors/%s/%s/state/%s/"
	defaultActorType         = "testactorfeatures"   // Actor type must be unique per test app.
	actorTypeEnvName         = "TEST_APP_ACTOR_TYPE" // Env variable tests can set to change actor type.
	actorIdleTimeout         = "1h"
	drainOngoingCallTimeout  = "30s"
	drainRebalancedActors    = true
	secondsToWaitInMethod    = 5
	postgresConnectionString = "host=dapr-postgres-postgresql.dapr-tests.svc.cluster.local user=postgres password=example port=5432 connect_timeout=10 database=dapr_test"
	reminderKeyPrefix        = "dapr"
)

var (
	httpClient     = utils.NewHTTPClient()
	postgresClient *pgx.Conn
	etcdClient     *clientv3.Client
)

type daprConfig struct {
	Entities                []string `json:"entities,omitempty"`
	ActorIdleTimeout        string   `json:"actorIdleTimeout,omitempty"`
	DrainOngoingCallTimeout string   `json:"drainOngoingCallTimeout,omitempty"`
	DrainRebalancedActors   bool     `json:"drainRebalancedActors,omitempty"`
}

var registeredActorType = map[string]bool{}

var daprConfigResponse = daprConfig{
	getActorType(),
	actorIdleTimeout,
	drainOngoingCallTimeout,
	drainRebalancedActors,
}

func getActorType() []string {
	actorType := os.Getenv(actorTypeEnvName)
	if actorType == "" {
		registeredActorType[defaultActorType] = true
		return []string{defaultActorType}
	}

	actorTypes := strings.Split(actorType, ",")
	for _, tp := range actorTypes {
		registeredActorType[tp] = true
	}
	return actorTypes
}

// indexHandler is the handler for root path
func indexHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("indexHandler is called")
	w.WriteHeader(http.StatusOK)
}

func configHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing Dapr request for %s, responding with %#v", r.URL.RequestURI(), daprConfigResponse)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(daprConfigResponse)
}

func deactivateActorHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s actor request for %s", r.Method, r.URL.RequestURI())
	actorType := chi.URLParam(r, "actorType")

	if !registeredActorType[actorType] {
		log.Printf("Unknown actor type: %s", actorType)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

func healthzHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

var actorReminderCount atomic.Uint32

func actorReminderHandler(w http.ResponseWriter, r *http.Request) {
	actorReminderCount.Add(1)
}

func actorRemindersCountHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(actorReminderCount.Load())
}

func createActorReminder(w http.ResponseWriter, r *http.Request) {
	log.Printf("Creating actor reminder for %s", r.URL.RequestURI())

	actorType := chi.URLParam(r, "actorType")
	actorID := chi.URLParam(r, "id")
	reminderName := chi.URLParam(r, "reminderName")

	endpoint := fmt.Sprintf("http://127.0.0.1:3500/v1.0/actors/%s/%s/reminders/%s", actorType, actorID, reminderName)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, r.Body)
	if err != nil {
		log.Printf("Error creating request: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "applcation/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error calling Dapr: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func actorMethodHandler(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing actor method request for %s", r.URL.RequestURI())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

func invokeActor(w http.ResponseWriter, r *http.Request) {
	log.Printf("Processing %s actor request for %s", r.Method, r.URL.RequestURI())

	actorType := chi.URLParam(r, "actorType")
	actorID := chi.URLParam(r, "id")
	method := chi.URLParam(r, "method")

	endpoint := fmt.Sprintf("http://127.0.0.1:3500/v1.0/actors/%s/%s/method/%s", actorType, actorID, method)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, endpoint, r.Body)
	if err != nil {
		log.Printf("Error creating request: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "applcation/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error calling Dapr: %s", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func cleanupHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)

	multiWriter := io.MultiWriter(w, os.Stdout)
	log.SetOutput(multiWriter)

	log.Printf("processing %s test request for %s", r.Method, r.URL.RequestURI())

	actorType := chi.URLParam(r, "actorType")
	log.Printf("cleaning up actors for type %s", actorType)

	cleanupPostgres(actorType)

	cleanupEtcd()
}

func cleanupEtcd() {
	log.Printf("looking for the existing actor type in etcd")

	timeout, cancelFunc := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFunc()

	list, err := etcdClient.Get(timeout, reminderKeyPrefix, clientv3.WithPrefix())
	if err != nil {
		log.Printf("failed to get %s prefix list from etcd: %v", reminderKeyPrefix, err)
		return
	}

	log.Printf("found %d existing reminders with prefix %s", list.Count, reminderKeyPrefix)

	if list.Count > 0 {
		timeoutDelete, cancelFuncDelete := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelFuncDelete()
		response, err := etcdClient.Delete(timeoutDelete, reminderKeyPrefix, clientv3.WithPrefix())
		if err != nil {
			log.Printf("failed to delete %s prefix list from etcd: %v", reminderKeyPrefix, err)
			return
		}
		log.Printf("deleted %d existing reminders with prefix %s", response.Deleted, reminderKeyPrefix)
	}
}

func cleanupPostgres(actorType string) {
	log.Printf("looking for the existing actor type in postgres")
	query := fmt.Sprintf("SELECT value FROM dapr_test.public.v2actorstate where key = 'actors||%s'", actorType)
	log.Printf("query: %s", query)
	rows, err := postgresClient.Query(context.Background(), query)
	if err != nil {
		log.Printf("error looking for the existing actor type: %s", err.Error())
		return
	}

	var values [][]byte
	for rows.Next() {
		var value []byte
		err := rows.Scan(&value)
		if err != nil {
			log.Printf("error scanning the existing actor type: %s", err.Error())
			return
		}
		values = append(values, value)
	}

	log.Printf("found %d existing actor type reminders rows", len(values))

	for _, value := range values {
		if len(value) > 0 {
			log.Printf("found %s reminders record", actorType)

			var reminders []any
			err = json.Unmarshal(value, &reminders)
			if err != nil {
				log.Printf("error unmarshalling the existing actor type: %+v", err)
				return
			}
			log.Printf("actor reminders contain: %d entries", len(reminders))

			if len(reminders) > 0 {
				log.Printf("deleting %s reminders entries", actorType)
				_, err := postgresClient.Exec(context.Background(), fmt.Sprintf("DELETE FROM dapr_test.public.v2actorstate"))
				if err != nil {
					log.Printf("error deleting actor type %s reminders entries: %+v", actorType, err)
					return
				}
				log.Printf("successfully deleted %s reminders record", actorType)
			}
		}
	}
}

// appRouter initializes restful api router
func appRouter() http.Handler {
	router := chi.NewRouter()

	router.Get("/", indexHandler)
	router.Get("/dapr/config", configHandler)

	router.HandleFunc("/actors/{actorType}/{id}/method/{method}", actorMethodHandler)
	router.Get("/actors/{actorType}/{id}/method/{method}", invokeActor)
	router.HandleFunc("/actors/{actorType}/{id}/method/remind/{reminderName}", actorReminderHandler)
	router.HandleFunc("/actors/{actorType}/{id}/reminders/{reminderName}", createActorReminder)
	router.HandleFunc("/remindersCount", actorRemindersCountHandler)

	router.Post("/actors/{actorType}/{id}", deactivateActorHandler)
	router.Delete("/actors/{actorType}/{id}", deactivateActorHandler)

	router.Get("/healthz", healthzHandler)

	router.HandleFunc("/test", fortioTestHandler)

	router.HandleFunc("/test/cleanup/{actorType}", cleanupHandler)

	return router
}

func initPgClient() error {
	connect, err := pgx.Connect(context.Background(), postgresConnectionString)
	if err != nil {
		log.Printf("Error connecting to postgres: %s", err.Error())
		return err
	}
	postgresClient = connect
	return nil
}

func initEtcdClient() error {
	schedulerEtcdEndpoint := "dapr-scheduler-server.dapr-tests.svc.cluster.local:2379"
	cl, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{schedulerEtcdEndpoint},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		return err
	}
	etcdClient = cl
	return nil
}

func main() {
	err := initPgClient()
	if err != nil {
		log.Printf("error initializing postgres client: %s", err.Error())
		return
	}

	err = initEtcdClient()
	if err != nil {
		log.Printf("Error initializing etcd client: %s", err.Error())
		return
	}

	log.Printf("Actor App - listening on http://localhost:%d", appPort)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appPort), appRouter()))
}
