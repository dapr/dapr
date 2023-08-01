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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/google/uuid"
)

func subscribeHandler(w http.ResponseWriter, r *http.Request) {
	subscriptions := []subscription{}

	subscribeType := os.Getenv("SUBSCRIBE_TYPE")
	if subscribeType == "bulk" {
		subscriptions = append(subscriptions, subscription{
			PubsubName: pubSubName,
			Topic:      topic + "-bulk",
			Route:      route + "-bulk",
			BulkSubscribe: bulkSubscribe{
				Enabled: true,
			},
		})
	} else {
		subscriptions = append(subscriptions, subscription{
			PubsubName: pubSubName,
			Topic:      topic,
			Route:      route,
		})
	}

	log.Printf("Sending subscriptions: %#v", subscriptions)

	jsonBytes, err := json.Marshal(subscriptions)
	if err != nil {
		log.Fatalf("Error marshalling subscriptions: %v", err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(jsonBytes)
	if err != nil {
		log.Fatalf("Error writing response: %v", err)
	}
}

func bulkMessageHandler(w http.ResponseWriter, r *http.Request) {
	postBody, err := io.ReadAll(r.Body)
	if err != nil {
		log.Fatalf("Error reading request body: %v", err)
	}

	var bulkSubscribeMessage bulkSubscribeMessage
	err = json.Unmarshal(postBody, &bulkSubscribeMessage)
	if err != nil {
		log.Fatalf("Error unmarshalling request body: %v", err)
	}

	//log.Printf("Received %d messages", len(bulkSubscribeMessage.Entries))

	var bulkSubscribeResponseStatuses []bulkSubscribeResponseStatus
	for _, entry := range bulkSubscribeMessage.Entries {
		messagesCh <- entry.EntryId
		bulkSubscribeResponseStatuses = append(bulkSubscribeResponseStatuses, bulkSubscribeResponseStatus{
			EntryID: entry.EntryId,
			Status:  "SUCCESS",
		})
	}

	resp := bulkSubscribeResponse{Statuses: bulkSubscribeResponseStatuses}
	jsonBytes, err := json.Marshal(resp)
	if err != nil {
		log.Fatalf("Error marshalling response: %v", err)
	}
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(jsonBytes)
	if err != nil {
		log.Fatalf("Error writing response: %v", err)
	}
}

func messageHandler(w http.ResponseWriter, r *http.Request) {
	_, err := io.ReadAll(r.Body)
	if err != nil {
		log.Fatalf("Error reading request body: %v", err)
	}

	//log.Printf("received 1 message")
	uuid, err := uuid.NewUUID()
	if err != nil {
		log.Fatalf("Error generating uuid: %v", err)
	}
	messagesCh <- uuid.String()

	w.WriteHeader(http.StatusOK)
	_, err = fmt.Fprint(w, "SUCCESS")
	if err != nil {
		log.Fatalf("Error writing response: %v", err)
	}
}
