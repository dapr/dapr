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
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	daprPort          = 3500
	pubsubTopic       = "runtime-pubsub-http"
	numPubsubMessages = 10
	bindingTopic      = "runtime-bindings-http"
	numBindingMessage = 10
)

var httpClient = utils.NewHTTPClient()

func publishMessagesToPubsub(wg *sync.WaitGroup) {
	defer wg.Done()

	daprPubsubURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s", daprPort, pubsubTopic)
	for i := 0; i < numPubsubMessages; i++ {
		m := fmt.Sprintf("message-%d", i)
		jsonValue, err := json.Marshal(m)
		if err != nil {
			log.Fatalf("Error marshalling %s to JSON", m)
		}
		log.Printf("Publishing to %s", daprPubsubURL)
		r, err := httpClient.Post(daprPubsubURL, "application/json", bytes.NewBuffer(jsonValue))
		if r != nil {
			defer r.Body.Close()
		}
		if err != nil {
			log.Fatalf("Error publishing messages to pubsub: %+v", err)
		}
	}
}

func publishMessagesToBinding(wg *sync.WaitGroup) {
	defer wg.Done()

	daprBindingURL := fmt.Sprintf("http://localhost:%d/v1.0/bindings/%s", daprPort, bindingTopic)
	for i := 0; i < numBindingMessage; i++ {
		b := []byte(fmt.Sprintf(`{"data": {"id": "message%d"}}`, i))
		log.Printf("Publishing to %s", daprBindingURL)
		r, err := httpClient.Post(daprBindingURL, "application/json", bytes.NewBuffer(b))
		if r != nil {
			defer r.Body.Close()
		}
		if err != nil {
			log.Fatalf("Error publishing messages to binding: %+v", err)
		}
	}
}

func main() {
	var wg sync.WaitGroup

	// Push messages onto pubsub
	wg.Add(1)
	go publishMessagesToPubsub(&wg)

	// Push messages onto binding
	wg.Add(1)
	go publishMessagesToBinding(&wg)

	wg.Wait()

	// Block until signalled to close
	exit := make(chan os.Signal, 1)
	signal.Notify(exit, os.Interrupt, syscall.SIGTERM)
	<-exit

	log.Println("Terminating!")
}
