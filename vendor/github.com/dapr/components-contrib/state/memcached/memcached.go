package memcached

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
	"github.com/dapr/components-contrib/state"
	jsoniter "github.com/json-iterator/go"
)

const (
	hosts              = "hosts"
	maxIdleConnections = "maxIdleConnections"
	timeout            = "timeout"
	// These defaults are already provided by gomemcache
	defaultMaxIdleConnections = 2
	defaultTimeout            = 1000 * time.Millisecond
)

type Memcached struct {
	client *memcache.Client
	json   jsoniter.API
}

type memcachedMetadata struct {
	hosts              []string
	maxIdleConnections int
	timeout            time.Duration
}

func NewMemCacheStateStore() *Memcached {
	return &Memcached{
		json: jsoniter.ConfigFastest,
	}
}

func (m *Memcached) Init(metadata state.Metadata) error {
	meta, err := getMemcachedMetadata(metadata)
	if err != nil {
		return err
	}

	client := memcache.New(meta.hosts...)
	client.Timeout = meta.timeout
	client.MaxIdleConns = meta.maxIdleConnections

	m.client = client

	err = client.Ping()
	if err != nil {
		return err
	}

	return nil
}

func getMemcachedMetadata(metadata state.Metadata) (*memcachedMetadata, error) {
	meta := memcachedMetadata{
		maxIdleConnections: defaultMaxIdleConnections,
		timeout:            defaultTimeout,
	}

	if val, ok := metadata.Properties[hosts]; ok && val != "" {
		meta.hosts = strings.Split(val, ",")
	} else {
		return nil, errors.New("missing or empty hosts field from metadata")
	}

	if val, ok := metadata.Properties[maxIdleConnections]; ok && val != "" {
		p, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("error parsing maxIdleConnections")
		}
		meta.maxIdleConnections = p
	}

	if val, ok := metadata.Properties[timeout]; ok && val != "" {
		p, err := strconv.Atoi(val)
		if err != nil {
			return nil, fmt.Errorf("error parsing timeout")
		}
		meta.timeout = time.Duration(p) * time.Millisecond
	}

	return &meta, nil
}

func (m *Memcached) setValue(req *state.SetRequest) error {
	var bt []byte
	b, ok := req.Value.([]byte)
	if ok {
		bt = b
	} else {
		bt, _ = m.json.Marshal(req.Value)
	}
	err := m.client.Set(&memcache.Item{Key: req.Key, Value: bt})

	if err != nil {
		return fmt.Errorf("failed to set key %s: %s", req.Key, err)
	}

	return nil
}

func (m *Memcached) Delete(req *state.DeleteRequest) error {
	err := m.client.Delete(req.Key)
	if err != nil {
		return err
	}
	return nil
}

func (m *Memcached) BulkDelete(req []state.DeleteRequest) error {
	for _, re := range req {
		err := m.Delete(&re)
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Memcached) Get(req *state.GetRequest) (*state.GetResponse, error) {
	item, err := m.client.Get(req.Key)
	if err != nil {
		// Return nil for status 204
		if err == memcache.ErrCacheMiss {
			return nil, nil
		}
		return &state.GetResponse{}, err
	}

	return &state.GetResponse{
		Data: item.Value,
	}, nil
}

func (m *Memcached) Set(req *state.SetRequest) error {
	return state.SetWithRetries(m.setValue, req)
}

func (m *Memcached) BulkSet(req []state.SetRequest) error {
	for _, r := range req {
		err := m.Set(&r)
		if err != nil {
			return err
		}
	}
	return nil
}
