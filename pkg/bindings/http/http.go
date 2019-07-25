package http

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/actionscore/actions/pkg/components/bindings"
)

// HTTPSource allows sending data to an HTTP URL
type HTTPSource struct {
	Spec Metadata
}

// Metadata is the config object for HTTPSource
type Metadata struct {
	URL    string `json:"url"`
	Method string `json:"method"`
}

// NewHTTP returns a new HTTPSource
func NewHTTP() *HTTPSource {
	return &HTTPSource{}
}

// Init performs metadata parsing
func (h *HTTPSource) Init(metadata bindings.Metadata) error {
	b, err := json.Marshal(metadata.ConnectionInfo)
	if err != nil {
		return err
	}

	var httpMetadata Metadata
	err = json.Unmarshal(b, &httpMetadata)
	if err != nil {
		return err
	}

	h.Spec = httpMetadata

	return nil
}

// HttpGet performs an HTTP get request
func (h *HTTPSource) HttpGet(url string) ([]byte, error) {
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

func (h *HTTPSource) Read(handler func(*bindings.ReadResponse) error) error {
	b, err := h.HttpGet(h.Spec.URL)
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
	resp, err := client.Post(h.Spec.URL, "application/json; charset=utf-8", bytes.NewBuffer(req.Data))
	if err != nil {
		return err
	}

	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	return nil
}
