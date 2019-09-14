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

// StateStore is a Redis state store
type StateStore struct {
	client *redis.SyncCtx
	json   jsoniter.API
}

type credenials struct {
	Host     string `json:"redisHost"`
	Password string `json:"redisPassword"`
}

// NewRedisStateStore returns a new redis state store
func NewRedisStateStore() *StateStore {
	return &StateStore{
		json: jsoniter.ConfigFastest,
	}
}

// Init does metadata and connection parsing
func (r *StateStore) Init(metadata state.Metadata) error {
	rand.Seed(time.Now().Unix())

	connInfo := metadata.Properties
	b, err := json.Marshal(connInfo)
	if err != nil {
		return err
	}

	var redisCreds credenials
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

// Delete performs a delete operation
func (r *StateStore) Delete(req *state.DeleteRequest) error {
	res := r.client.Do(context.Background(), "DEL", req.Key)
	if err := redis.AsError(res); err != nil {
		return err
	}

	return nil
}

// BulkDelete performs a bulk delete operation
func (r *StateStore) BulkDelete(req []state.DeleteRequest) error {
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

// Get retrieves state from redis with a key
func (r *StateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
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

// Set saves state into redis
func (r *StateStore) Set(req *state.SetRequest) error {
	b, _ := r.json.Marshal(req.Value)
	res := r.client.Do(context.Background(), "SET", req.Key, b)
	if err := redis.AsError(res); err != nil {
		return err
	}

	return nil
}

// BulkSet performs a bulks save operation
func (r *StateStore) BulkSet(req []state.SetRequest) error {
	for _, s := range req {
		err := r.Set(&s)
		if err != nil {
			return err
		}
	}

	return nil
}

// Multi performs a transactional operation. succeeds only if all operations succeed, and fails if one or more operations fail
func (r *StateStore) Multi(operations []state.TransactionalRequest) error {
	redisReqs := []redis.Request{}
	for _, o := range operations {
		if o.Operation == state.Upsert {
			req := o.Request.(state.SetRequest)
			b, _ := r.json.Marshal(req.Value)
			redisReqs = append(redisReqs, redis.Req("SET", req.Key, b))
		} else if o.Operation == state.Delete {
			req := o.Request.(state.DeleteRequest)
			redisReqs = append(redisReqs, redis.Req("DEL", req.Key))
		}
	}

	_, err := r.client.SendTransaction(context.Background(), redisReqs)
	return err
}
