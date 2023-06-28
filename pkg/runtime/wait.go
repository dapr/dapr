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

//nolint:forbidigo
package runtime

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

var (
	timeoutSeconds       = 60
	requestTimeoutMillis = 500
	periodMillis         = 100
	urlFormat            = "http://localhost:%s/v1.0/healthz/outbound"
)

func WaitUntilDaprOutboundReady(daprHTTPPort string) {
	outboundReadyHealthURL := fmt.Sprintf(urlFormat, daprHTTPPort)
	client := &http.Client{
		Timeout: time.Duration(requestTimeoutMillis) * time.Millisecond,
	}
	println(fmt.Sprintf("Waiting for Dapr to be outbound ready (timeout: %d seconds): url=%s\n", timeoutSeconds, outboundReadyHealthURL))

	var err error
	timeoutAt := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	lastPrintErrorTime := time.Now()
	for time.Now().Before(timeoutAt) {
		err = checkIfOutboundReady(client, outboundReadyHealthURL)
		if err == nil {
			println("Dapr is outbound ready!")
			return
		}

		if time.Now().After(lastPrintErrorTime) {
			// print the error once in one seconds to avoid too many errors
			lastPrintErrorTime = time.Now().Add(time.Second)
			println(fmt.Sprintf("Dapr outbound NOT ready yet: %v", err))
		}

		time.Sleep(time.Duration(periodMillis) * time.Millisecond)
	}

	println(fmt.Sprintf("timeout waiting for Dapr to become outbound ready. Last error: %v", err))
}

func checkIfOutboundReady(client *http.Client, outboundReadyHealthURL string) error {
	req, err := http.NewRequest(http.MethodGet, outboundReadyHealthURL, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("HTTP status code %v", resp.StatusCode)
	}

	return nil
}
