package config

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v2"
)

type Configuration struct {
	Spec ConfigurationSpec `json:"spec,omitempty"`
}

type ConfigurationSpec struct {
	TracingSpec TracingSpec `json:"tracing,omitempty"`
}

type TracingSpec struct {
	Enabled          bool   `json:"enabled"`
	TracerType       string `json:"tracerType"`
	ExporterType     string `json:"exporterType"`
	ExporterAddress  string `json:"exporterAddress"`
	IncludeEvent     bool   `json:"includeEvent"`
	IncludeEventBody bool   `json:"includeEventBody"`
}

const (
	NullTracer       = "NullTracer"
	ConsoleTracer    = "ConsoleTracer"
	OpenCensusTracer = "OpenCensusTracer"
)

func LoadDefaultConfiguration() *Configuration {
	return &Configuration{
		Spec: ConfigurationSpec{
			TracingSpec: TracingSpec{
				Enabled:    false,
				TracerType: NullTracer,
			},
		},
	}
}

func LoadStandaloneConfiguration(config string) (*Configuration, error) {
	_, err := os.Stat(config)
	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadFile(config)
	if err != nil {
		return nil, err
	}

	var conf Configuration
	err = yaml.Unmarshal(b, &conf)
	if err != nil {
		return nil, err
	}

	return &conf, nil
}

func LoadKubernetesConfiguration(config, controlPlaneAddress string) (*Configuration, error) {
	url := fmt.Sprintf("%s/configurations/%s", controlPlaneAddress, config)
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

	var conf Configuration
	err = json.Unmarshal(body, &conf)
	if err != nil {
		return nil, err
	}

	return &conf, nil
}
