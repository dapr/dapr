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
	Spec ConfigurationSpec `json:"spec" yaml:"spec"`
}

type ConfigurationSpec struct {
	TracingSpec TracingSpec `json:"tracing,omitempty" yaml:"tracing,omitempty"`
}

type TracingSpec struct {
	Enabled         bool   `json:"enabled" yaml:"enabled"`
	ExporterType    string `json:"exporterType" yaml:"exporterType"`
	ExporterAddress string `json:"exporterAddress" yaml:"exporterAddress"`
	ExpandParams    bool   `json:"expandParams" yaml:"expandParams"`
	IncludeBody     bool   `json:"includeBody" yaml:"includeBody"`
}

func LoadDefaultConfiguration() *Configuration {
	return &Configuration{
		Spec: ConfigurationSpec{
			TracingSpec: TracingSpec{
				Enabled: false,
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
