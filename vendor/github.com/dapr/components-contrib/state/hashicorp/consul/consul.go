// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package consul

import (
	"encoding/json"
	"fmt"

	"github.com/dapr/components-contrib/state"
	"github.com/hashicorp/consul/api"
	"github.com/pkg/errors"
)

// Consul is a state store implementation for HashiCorp Consul.
type Consul struct {
	client        *api.Client
	keyPrefixPath string
}

type consulConfig struct {
	Datacenter    string `json:"datacenter"`
	HTTPAddr      string `json:"httpAddr"`
	ACLToken      string `json:"aclToken"`
	Scheme        string `json:"scheme"`
	KeyPrefixPath string `json:"keyPrefixPath"`
}

// NewConsulStateStore returns a new consul state store.
func NewConsulStateStore() *Consul {
	return &Consul{}
}

// Init does metadata and config parsing and initializes the
// Consul client
func (c *Consul) Init(metadata state.Metadata) error {
	consulConfig, err := metadataToConfig(metadata.Properties)
	if err != nil {
		return fmt.Errorf("couldn't convert metadata properties: %s", err)
	}

	var keyPrefixPath string
	if consulConfig.KeyPrefixPath == "" {
		keyPrefixPath = "dapr"
	}

	config := &api.Config{
		Datacenter: consulConfig.Datacenter,
		Address:    consulConfig.HTTPAddr,
		Token:      consulConfig.ACLToken,
		Scheme:     consulConfig.Scheme,
	}

	client, err := api.NewClient(config)
	if err != nil {
		return errors.Wrap(err, "initializing consul client")
	}

	c.client = client
	c.keyPrefixPath = keyPrefixPath

	return nil
}

func metadataToConfig(connInfo map[string]string) (*consulConfig, error) {
	b, err := json.Marshal(connInfo)
	if err != nil {
		return nil, err
	}

	var config consulConfig
	err = json.Unmarshal(b, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// Get retrieves a Consul KV item
func (c *Consul) Get(req *state.GetRequest) (*state.GetResponse, error) {
	queryOpts := &api.QueryOptions{}
	if req.Options.Consistency == state.Strong {
		queryOpts.RequireConsistent = true
	}

	resp, queryMeta, err := c.client.KV().Get(fmt.Sprintf("%s/%s", c.keyPrefixPath, req.Key), queryOpts)
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return &state.GetResponse{}, nil
	}

	return &state.GetResponse{
		Data: resp.Value,
		ETag: queryMeta.LastContentHash,
	}, nil
}

// Set saves a Consul KV item
func (c *Consul) Set(req *state.SetRequest) error {
	var reqValByte []byte
	b, ok := req.Value.([]byte)
	if ok {
		reqValByte = b
	} else {
		reqValByte, _ = json.Marshal(req.Value)
	}

	keyWithPath := fmt.Sprintf("%s/%s", c.keyPrefixPath, req.Key)

	_, err := c.client.KV().Put(&api.KVPair{
		Key:   keyWithPath,
		Value: reqValByte}, nil)

	if err != nil {
		return fmt.Errorf("couldn't set key %s: %s", keyWithPath, err)
	}

	return nil
}

// BulkSet performs a bulk save operation
func (c *Consul) BulkSet(req []state.SetRequest) error {
	for _, s := range req {
		err := c.Set(&s)
		if err != nil {
			return err
		}
	}

	return nil
}

// Delete performes a Consul KV delete operation
func (c *Consul) Delete(req *state.DeleteRequest) error {
	keyWithPath := fmt.Sprintf("%s/%s", c.keyPrefixPath, req.Key)
	_, err := c.client.KV().Delete(keyWithPath, nil)
	if err != nil {
		return fmt.Errorf("couldn't delete key %s: %s", keyWithPath, err)
	}

	return nil
}

// BulkDelete performs a bulk delete operation
func (c *Consul) BulkDelete(req []state.DeleteRequest) error {
	for _, re := range req {
		err := c.Delete(&re)
		if err != nil {
			return err
		}
	}

	return nil
}
