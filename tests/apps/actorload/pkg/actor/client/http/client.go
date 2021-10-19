// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	cl "actorload/pkg/actor/client"
)

const (
	apiVersion      = "v1.0"
	defaultHTTPPort = "3500"
	defaultHost     = "127.0.0.1"
)

type DaprActorClientError struct {
	Code    int
	Message string
}

func (e *DaprActorClientError) Error() string {
	return fmt.Sprintf("%d %s", e.Code, e.Message)
}

type httpClient struct {
	address string
	client  http.Client
}

type TransactionalRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type TransactionalStateOperation struct {
	Operation string               `json:"operation"`
	Request   TransactionalRequest `json:"request"`
}

func NewClient() cl.ActorClient {
	port := os.Getenv("DAPR_HTTP_PORT")
	if port == "" {
		port = defaultHTTPPort
	}
	return &httpClient{
		address: fmt.Sprintf("http://%s:%s", defaultHost, port),
		client:  http.Client{},
	}
}

func (c *httpClient) defaultURL(actorType, actorID string) string {
	return fmt.Sprintf("%s/%s/actors/%s/%s", c.address, apiVersion, actorType, actorID)
}

func (c *httpClient) InvokeMethod(actorType, actorID, method string, contentType string, data []byte) ([]byte, error) {
	url := fmt.Sprintf("%s/method/%s", c.defaultURL(actorType, actorID), method)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, &DaprActorClientError{Code: resp.StatusCode, Message: string(body)}
	}

	return body, nil
}

func (c *httpClient) SaveStateTransactionally(actorType, actorID string, data []byte) error {
	url := fmt.Sprintf("%s/state", c.defaultURL(actorType, actorID))
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json; utf-8")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode >= 300 {
		return &DaprActorClientError{Code: resp.StatusCode, Message: string(body)}
	}

	return nil
}

func (c *httpClient) GetState(actorType, actorID, name string) ([]byte, error) {
	url := fmt.Sprintf("%s/state/%s", c.defaultURL(actorType, actorID), name)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 300 {
		return nil, &DaprActorClientError{Code: resp.StatusCode, Message: string(body)}
	}

	return body, nil
}

func (c *httpClient) WaitUntilDaprIsReady() error {
	for i := 0; i < 10; i++ {
		resp, err := c.client.Get(fmt.Sprintf("%s/%s", c.address, "v1.0/healthz"))
		if err == nil && resp.StatusCode == 204 {
			return nil
		}
		time.Sleep(time.Millisecond * 500)
	}

	return errors.New("dapr is unavailable")
}

func (c *httpClient) Close() {
	c.client.CloseIdleConnections()
}
