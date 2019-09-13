package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/actionscore/actions/pkg/components/state"

	"github.com/joomcode/redispipe/redis"
	"github.com/joomcode/redispipe/redisconn"
	jsoniter "github.com/json-iterator/go"
)

const (
	setQuery = "local var1 = redis.pcall(\"HGET\", KEYS[1], \"version\"); if type(var1) == \"table\" then redis.call(\"DEL\", KEYS[1]); end; if not var1 or type(var1)==\"table\" or var1 == \"\" or var1 == ARGV[1] then redis.call(\"HSET\", KEYS[1], \"data\", ARGV[2]) return redis.call(\"HINCRBY\", KEYS[1], \"version\", 1) else return error(\"failed to set key \" .. KEYS[1]) end"
	delQuery = "local var1 = redis.pcall(\"HGET\", KEYS[1], \"version\"); if not var1 or type(var1)==\"table\" or var1 == ARGV[1] then return redis.call(\"DEL\", KEYS[1]) else return error(\"failed to delete \" .. KEYS[1]) end"
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

	connInfo := metadata.ConnectionInfo
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

	res := r.client.Do(context.Background(), "EVAL", delQuery, 1, req.Key, req.ETag)

	if err := redis.AsError(res); err != nil {
		return fmt.Errorf("failed to delete key '%s' due to ETag mismatch", req.Key)
	}

	return nil
}

// BulkDelete performs a bulk delete operation
func (r *StateStore) BulkDelete(req []state.DeleteRequest) error {
	for _, re := range req {
		err := r.Delete(&re)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *StateStore) directGet(req *state.GetRequest) (*state.GetResponse, error) {
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

// Get retrieves state from redis with a key
func (r *StateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	res := r.client.Do(context.Background(), "HGETALL", req.Key) // Prefer values with ETags
	if err := redis.AsError(res); err != nil {
		return r.directGet(req) //Falls back to original get
	}
	if res == nil {
		return &state.GetResponse{}, nil
	}
	vals := res.([]interface{})
	if len(vals) == 0 {
		return &state.GetResponse{}, nil
	}

	data, version, err := r.getKeyVersion(vals)
	if err != nil {
		return nil, err
	}

	s, _ := strconv.Unquote(data)

	return &state.GetResponse{
		Data: []byte(s),
		ETag: version,
	}, nil
}

// Set saves state into redis
func (r *StateStore) Set(req *state.SetRequest) error {
	b, _ := r.json.Marshal(req.Value)

	ver, err := r.parseETag(req.ETag)
	if err != nil {
		return err
	}

	res := r.client.Do(context.Background(), "EVAL", setQuery, 1, req.Key, ver, b)
	if err := redis.AsError(res); err != nil {
		return fmt.Errorf("failed to set key '%s' due to ETag mismatch", req.Key)
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

func (r *StateStore) getKeyVersion(vals []interface{}) (data string, version string, err error) {
	seenData := false
	seenVersion := false
	for i := 0; i < len(vals); i += 2 {
		field, _ := strconv.Unquote(fmt.Sprintf("%q", vals[i]))
		switch field {
		case "data":
			data, _ = strconv.Unquote(fmt.Sprintf("%q", vals[i+1]))
			seenData = true
		case "version":
			version, _ = strconv.Unquote(fmt.Sprintf("%q", vals[i+1]))
			seenVersion = true
		}
	}
	if !seenData || !seenVersion {
		return "", "", errors.New("required hash field 'data' or 'version' was not found")
	}
	return data, version, nil
}

func (r *StateStore) parseETag(etag string) (int, error) {
	ver := 0
	var err error
	if etag != "" {
		ver, err = strconv.Atoi(etag)
		if err != nil {
			return -1, err
		}
	}
	return ver, nil
}
