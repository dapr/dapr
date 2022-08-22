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
	"io"
	"log"
	"os"
	"time"

	"github.com/dapr/dapr/tests/apps/utils"
)

const (
	daprPort       = 3500
	pubsubName     = "messagebus"
	pubsubTopic    = "pubsub-job-topic-http"
	message        = "message-from-job"
	publishRetries = 10
)

var httpClient = utils.NewHTTPClient()

func stopSidecar() {
	log.Printf("Shutting down the sidecar at %s", fmt.Sprintf("http://localhost:%d/v1.0/shutdown", daprPort))
	for retryCount := 0; retryCount < 200; retryCount++ {
		r, err := httpClient.Post(fmt.Sprintf("http://localhost:%d/v1.0/shutdown", daprPort), "", bytes.NewBuffer([]byte{}))
		if r != nil {
			// Drain before closing
			_, _ = io.Copy(io.Discard, r.Body)
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
	r, err := httpClient.Post(daprPubsubURL, "application/json", bytes.NewBuffer(jsonValue))
	if r != nil {
		// Drain before closing
		_, _ = io.Copy(io.Discard, r.Body)
		r.Body.Close()
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
