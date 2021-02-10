// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

const (
	daprPort          = 3500
	pubsubTopic       = "runtime-pubsub-http"
	numPubsubMessages = 10
	bindingTopic      = "runtime-bindings-http"
	numBindingMessage = 10
)

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
		// nolint: gosec
		r, err := http.Post(daprPubsubURL, "application/json", bytes.NewBuffer(jsonValue))
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
		// nolint: gosec
		r, err := http.Post(daprBindingURL, "application/json", bytes.NewBuffer(b))
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
