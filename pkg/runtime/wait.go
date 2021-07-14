// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

var (
	timeoutSeconds       int    = 60
	requestTimeoutMillis int    = 500
	periodMillis         int    = 100
	urlFormat            string = "http://localhost:%s/v1.0/healthz/outbound"
)

func waitUntilDaprOutboundReady(daprHTTPPort string) error {
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
			return nil
		}

		if time.Now().After(lastPrintErrorTime) {
			// print the error once in one seconds to avoid too many errors
			lastPrintErrorTime = time.Now().Add(time.Second)
			println(fmt.Sprintf("Dapr outbound NOT ready yet: %v", err))
		}

		time.Sleep(time.Duration(periodMillis) * time.Millisecond)
	}

	println(fmt.Sprintf("timeout waiting for Dapr to become outbound ready. Last error: %v", err))
	return err
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
	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != 204 {
		return fmt.Errorf("HTTP status code %v", resp.StatusCode)
	}

	return nil
}
