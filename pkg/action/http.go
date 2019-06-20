package action

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"time"
)

type HttpSource struct {
	Spec HttpMetadata
}

type HttpMetadata struct {
	URL    string `json:"url"`
	Method string `json:"method"`
}

func NewHttpSource() *HttpSource {
	return &HttpSource{}
}

func (h *HttpSource) Init(eventSourceSpec EventSourceSpec) error {
	b, err := json.Marshal(eventSourceSpec.ConnectionInfo)
	if err != nil {
		return err
	}

	var httpMetadata HttpMetadata
	err = json.Unmarshal(b, &httpMetadata)
	if err != nil {
		return err
	}

	h.Spec = httpMetadata

	return nil
}

func (h *HttpSource) HttpGet(url string) ([]byte, error) {
	client := http.Client{Timeout: time.Second * 5}
	resp, err := client.Get(h.Spec.URL)
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

func (h *HttpSource) Read(metadata interface{}) (interface{}, error) {
	var data interface{}
	b, err := h.HttpGet(h.Spec.URL)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(b, &data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (h *HttpSource) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	data, err := h.HttpGet(h.Spec.URL)
	if err != nil {
		return err
	}

	return callback(data)
}

func (h *HttpSource) Write(data interface{}) error {
	b := new(bytes.Buffer)
	json.NewEncoder(b).Encode(data)

	client := http.Client{Timeout: time.Second * 5}
	resp, err := client.Post(h.Spec.URL, "application/json; charset=utf-8", b)
	if err != nil {
		return err
	}

	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	return nil
}
