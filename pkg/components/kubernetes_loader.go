package components

import (
	"encoding/json"
	"fmt"
	"time"

	config "github.com/actionscore/actions/pkg/config/modes"
	"github.com/valyala/fasthttp"
)

type KubernetesComponents struct {
	config config.KubernetesConfig
}

func NewKubernetesComponents(configuration config.KubernetesConfig) *KubernetesComponents {
	return &KubernetesComponents{
		config: configuration,
	}
}

func (k *KubernetesComponents) LoadComponents() ([]Component, error) {
	url := fmt.Sprintf("%s/components", k.config.ControlPlaneAddress)
	req := fasthttp.AcquireRequest()
	req.SetRequestURI(url)
	req.Header.SetContentType("application/json")

	resp := fasthttp.AcquireResponse()
	client := &fasthttp.Client{
		ReadTimeout: time.Second * 10,
	}
	err := client.Do(req, resp)
	if err != nil {
		return nil, err
	}

	body := resp.Body()

	var components []Component
	err = json.Unmarshal(body, &components)
	if err != nil {
		return nil, err
	}

	return components, nil
}
