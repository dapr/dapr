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
	"time"
)

const (
	daprPort       = 3500
	pubsubName     = "messagebus"
	pubsubTopic    = "pubsub-job-topic-http"
	message        = "message-from-job"
	publishRetries = 10
)

func stopSidecar() {
	log.Printf("Shutting down the sidecar at %s", fmt.Sprintf("http://localhost:%d/v1.0/shutdown", daprPort))
	for retryCount := 0; retryCount < 200; retryCount++ {
		r, err := http.Post(fmt.Sprintf("http://localhost:%d/v1.0/shutdown", daprPort), "", bytes.NewBuffer([]byte{}))
		if r != nil {
			r.Body.Close()
		}
		if err != nil {
			log.Printf("Error stopping the sidecar %s", err)
		}

		if r.StatusCode != 200 && r.StatusCode != 204 {
			log.Printf("Received Non-200 from shutdown API. Code: %d", r.StatusCode)
			time.Sleep(10 * time.Second)
			continue
		}
		break
	}
	log.Printf("Sidecar stopped")
}

func publishMessagesToPubsub() error {
	daprPubsubURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/%s/%s", daprPort, pubsubName, pubsubTopic)
	jsonValue, err := json.Marshal(message)
	if err != nil {
		log.Printf("Error marshalling %s to JSON", message)
	}
	log.Printf("Publishing to %s", daprPubsubURL)
	// nolint: gosec
	r, err := http.Post(daprPubsubURL, "application/json", bytes.NewBuffer(jsonValue))
	if r != nil {
		defer r.Body.Close()
	}
	if err != nil {
		log.Printf("Error publishing messages to pubsub: %+v", err)
	}
	return err
}

func main() {
	for retryCount := 0; retryCount < publishRetries; retryCount++ {
		err := publishMessagesToPubsub()
		if err != nil {
			log.Printf("Unable to publish, retrying.")
			time.Sleep(10 * time.Second)
		} else {
			// Wait for a minute before shutting down to give time for any validation by E2E test code.
			time.Sleep(1 * time.Minute)
			stopSidecar()
			os.Exit(0)
		}
	}
	stopSidecar()
	os.Exit(1)
}
