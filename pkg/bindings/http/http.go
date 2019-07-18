package http

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/actionscore/actions/pkg/components/bindings"
)

type HttpSource struct {
	Spec HttpMetadata
}

type HttpMetadata struct {
	URL    string `json:"url"`
	Method string `json:"method"`
}

func NewHTTP() *HttpSource {
	return &HttpSource{}
}

func (h *HttpSource) Init(metadata bindings.Metadata) error {
	b, err := json.Marshal(metadata.ConnectionInfo)
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

func (h *HttpSource) Read(handler func(*bindings.ReadResponse) error) error {
	b, err := h.HttpGet(h.Spec.URL)
	if err != nil {
		return err
	}

	handler(&bindings.ReadResponse{
		Data: b,
	})

	return nil
}

func (h *HttpSource) Write(req *bindings.WriteRequest) error {
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
