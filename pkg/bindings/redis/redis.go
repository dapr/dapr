package redis

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/actionscore/actions/pkg/components/bindings"

	"github.com/google/uuid"
	"github.com/joomcode/redispipe/redis"
	"github.com/joomcode/redispipe/redisconn"
)

type Redis struct {
	client *redis.SyncCtx
}

type RedisCredentials struct {
	Host     string `json:"redisHost"`
	Password string `json:"redisPassword"`
}

func NewRedis() *Redis {
	return &Redis{}
}

func (r *Redis) Init(metadata bindings.Metadata) error {
	connInfo := metadata.ConnectionInfo
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

func (r *Redis) Write(req *bindings.WriteRequest) error {
	key := fmt.Sprintf("es_%s", uuid.New().String())
	res := r.client.Do(context.Background(), "SET", key, req.Data)
	if err := redis.AsError(res); err != nil {
		return err
	}

	return nil
}
