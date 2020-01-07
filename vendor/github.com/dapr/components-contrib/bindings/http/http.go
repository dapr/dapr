// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/dapr/components-contrib/bindings"
)

// HTTPSource is a binding for an http url endpoint invocation
// nolint:golint
type HTTPSource struct {
	metadata httpMetadata
}

type httpMetadata struct {
	URL    string `json:"url"`
	Method string `json:"method"`
}

// NewHTTP returns a new HTTPSource
func NewHTTP() *HTTPSource {
	return &HTTPSource{}
}

// Init performs metadata parsing
func (h *HTTPSource) Init(metadata bindings.Metadata) error {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return err
	}

	var m httpMetadata
	err = json.Unmarshal(b, &m)
	if err != nil {
		return err
	}

	h.metadata = m
	return nil
}

func (h *HTTPSource) get(url string) ([]byte, error) {
	client := http.Client{Timeout: time.Second * 60}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	return b, nil
}

func (h *HTTPSource) Read(handler func(*bindings.ReadResponse) error) error {
	b, err := h.get(h.metadata.URL)
	if err != nil {
		return err
	}

	handler(&bindings.ReadResponse{
		Data: b,
	})
	return nil
}

func (h *HTTPSource) Write(req *bindings.WriteRequest) error {
	client := http.Client{Timeout: time.Second * 5}
	resp, err := client.Post(h.metadata.URL, "application/json; charset=utf-8", bytes.NewBuffer(req.Data))
	if err != nil {
		return err
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	return nil
}
