package action

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/joomcode/redispipe/redis"
	"github.com/joomcode/redispipe/redisconn"
	jsoniter "github.com/json-iterator/go"
)

type RedisStateStore struct {
	Client *redis.SyncCtx
	json   jsoniter.API
}

type RedisCredentials struct {
	Host     string `json:"redisHost"`
	Password string `json:"redisPassword"`
}

func NewRedisStateStore(j jsoniter.API) *RedisStateStore {
	return &RedisStateStore{
		json: j,
	}
}

func (r *RedisStateStore) Init(eventSourceSpec EventSourceSpec) error {
	rand.Seed(time.Now().Unix())

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

	r.Client = &redis.SyncCtx{
		S: conn,
	}

	return nil
}

func (r *RedisStateStore) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	return nil
}

func (r *RedisStateStore) GetAll(keyMatch string) ([]KeyValState, error) {
	state := []KeyValState{}
	scanner := r.Client.Scanner(context.Background(), redis.ScanOpts{Match: fmt.Sprintf("*%s*", keyMatch)})
	for {
		keys, err := scanner.Next()
		if err != nil {
			if err != redis.ScanEOF {
				return state, err
			}
			break
		}

		for _, k := range keys {
			s, _ := r.Read(k)
			state = append(state, KeyValState{
				Key:   strings.Replace(k, fmt.Sprintf("%s-", keyMatch), "", -1),
				Value: s,
			})
		}
	}
	return state, nil
}

func (r *RedisStateStore) Read(metadata interface{}) (interface{}, error) {
	key := metadata.(string)

	res := r.Client.Do(context.Background(), "GET", key)
	if err := redis.AsError(res); err != nil {
		return nil, err
	}

	if res == nil {
		return "", nil
	}

	return fmt.Sprintf("%q", res), nil
}

func (r *RedisStateStore) Write(data interface{}) error {
	state := data.(KeyValState)

	bytes, _ := jsoniter.Marshal(state.Value)
	res := r.Client.Do(context.Background(), "SET", state.Key, bytes)
	if err := redis.AsError(res); err != nil {
		return err
	}

	return nil
}
