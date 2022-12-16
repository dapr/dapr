/*
Copyright 2022 The Dapr Authors
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
)

func subscribeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("subscribeHandler called")
	subscriptions := []subscription{
		{
			PubsubName: pubSubName,
			Topic:      topic,
			Route:      route,
			Metadata:   map[string]string{"bulkSubscribe": "true"},
		},
	}
	jsonBytes, err := json.Marshal(subscriptions)
	if err != nil {
		log.Fatalf("error marshalling subscriptions: %s", err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(jsonBytes)
	if err != nil {
		log.Fatalf("error writing response: %s", err)
	}
}

func messageHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("messageHandler called")
	postBody, err := io.ReadAll(r.Body)
	if err != nil {
		log.Fatalf("error reading request body: %s", err)
	}

	var bulkSubscribeMessage bulkSubscribeMessage
	err = json.Unmarshal(postBody, &bulkSubscribeMessage)
	if err != nil {
		log.Fatalf("error unmarshalling request body: %s", err)
	}

	log.Printf("received %d messages", len(bulkSubscribeMessage.Entries))
	messagesCh <- len(bulkSubscribeMessage.Entries)

	var bulkSubscribeResponseStatuses []bulkSubscribeResponseStatus
	for _, entry := range bulkSubscribeMessage.Entries {
		bulkSubscribeResponseStatuses = append(bulkSubscribeResponseStatuses, bulkSubscribeResponseStatus{
			EntryID: entry.EntryId,
			Status:  "SUCCESS",
		})
	}

	resp := bulkSubscribeResponse{Statuses: bulkSubscribeResponseStatuses}
	jsonBytes, err := json.Marshal(resp)
	if err != nil {
		log.Fatalf("error marshalling response: %s", err)
	}
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(jsonBytes)
	if err != nil {
		log.Fatalf("error writing response: %s", err)
	}
}
