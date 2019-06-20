package action

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/joomcode/redispipe/redis"
	"github.com/joomcode/redispipe/redisconn"
)

type Redis struct {
	client *redis.SyncCtx
}

func NewRedis() *Redis {
	return &Redis{}
}

func (r *Redis) Init(eventSourceSpec EventSourceSpec) error {
	connInfo := eventSourceSpec.ConnectionInfo
	b, err := json.Marshal(connInfo)
	if err != nil {
		return err
	}

	var redisCreds RedisCredentials
	err = json.Unmarshal(b, &redisCreds)
	if err != nil {
		return err
	}

	ctx := context.Background()
	opts := redisconn.Opts{
		DB:       0,
		Password: redisCreds.Password,
	}
	conn, err := redisconn.Connect(ctx, redisCreds.Host, opts)
	if err != nil {
		return err
	}

	r.client = &redis.SyncCtx{
		S: conn,
	}

	return nil
}

func (r *Redis) Write(data interface{}) error {
	key := fmt.Sprintf("es_%s", uuid.New().String())
	value := fmt.Sprintf("%v", data)

	res := r.client.Do(context.Background(), "SET", key, value)
	if err := redis.AsError(res); err != nil {
		return err
	}

	return nil
}

func (r *Redis) Read(metadata interface{}) (interface{}, error) {
	return nil, nil
}

func (r *Redis) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	return nil
}
