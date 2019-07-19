package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/actionscore/actions/pkg/components/state"

	"github.com/joomcode/redispipe/redis"
	"github.com/joomcode/redispipe/redisconn"
	jsoniter "github.com/json-iterator/go"
)

type RedisStateStore struct {
	client *redis.SyncCtx
	json   jsoniter.API
}

type RedisCredentials struct {
	Host     string `json:"redisHost"`
	Password string `json:"redisPassword"`
}

func NewRedisStateStore() *RedisStateStore {
	return &RedisStateStore{
		json: jsoniter.ConfigFastest,
	}
}

func (r *RedisStateStore) Init(metadata state.Metadata) error {
	rand.Seed(time.Now().Unix())

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

func (r *RedisStateStore) Delete(req *state.DeleteRequest) error {
	res := r.client.Do(context.Background(), "DEL", req.Key)
	if err := redis.AsError(res); err != nil {
		return err
	}

	return nil
}

func (r *RedisStateStore) BulkDelete(req []state.DeleteRequest) error {
	keys := make([]interface{}, len(req))
	for i, r := range req {
		keys[i] = r
	}

	res := r.client.Do(context.Background(), "DEL", keys...)
	if err := redis.AsError(res); err != nil {
		return err
	}

	return nil
}

func (r *RedisStateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	res := r.client.Do(context.Background(), "GET", req.Key)
	if err := redis.AsError(res); err != nil {
		return nil, err
	}

	if res == nil {
		return &state.GetResponse{}, nil
	}
	s, _ := strconv.Unquote(fmt.Sprintf("%q", res))

	return &state.GetResponse{
		Data: []byte(s),
	}, nil
}

func (r *RedisStateStore) Set(req *state.SetRequest) error {
	b, _ := r.json.Marshal(req.Value)
	res := r.client.Do(context.Background(), "SET", req.Key, b)
	if err := redis.AsError(res); err != nil {
		return err
	}

	return nil
}

func (r *RedisStateStore) BulkSet(req []state.SetRequest) error {
	for _, s := range req {
		err := r.Set(&s)
		if err != nil {
			return err
		}
	}

	return nil
}
