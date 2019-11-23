// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package redis

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/dapr/components-contrib/bindings"

	"github.com/joomcode/redispipe/redis"
	"github.com/joomcode/redispipe/redisconn"
)

// Redis is a redis output binding
type Redis struct {
	client *redis.SyncCtx
}

type redisMetadata struct {
	Host     string `json:"redisHost"`
	Password string `json:"redisPassword"`
}

// NewRedis returns a new redis bindings instance
func NewRedis() *Redis {
	return &Redis{}
}

// Init performs metadata parsing and connection creation
func (r *Redis) Init(metadata bindings.Metadata) error {
	m, err := r.parseMetadata(metadata)
	if err != nil {
		return err
	}
	ctx := context.Background()
	opts := redisconn.Opts{
		DB:       0,
		Password: m.Password,
	}
	conn, err := redisconn.Connect(ctx, m.Host, opts)
	if err != nil {
		return err
	}
	r.client = &redis.SyncCtx{
		S: conn,
	}
	return nil
}

func (r *Redis) parseMetadata(metadata bindings.Metadata) (*redisMetadata, error) {
	connInfo := metadata.Properties
	b, err := json.Marshal(connInfo)
	if err != nil {
		return nil, err
	}

	var m redisMetadata
	err = json.Unmarshal(b, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *Redis) Write(req *bindings.WriteRequest) error {
	if val, ok := req.Metadata["key"]; ok && val != "" {
		key := val
		res := r.client.Do(context.Background(), "SET", key, req.Data)
		if err := redis.AsError(res); err != nil {
			return err
		}
		return nil
	}
	return errors.New("redis binding: missing key on write request metadata")
}
