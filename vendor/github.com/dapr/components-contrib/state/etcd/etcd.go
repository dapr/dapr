// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package etcd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dapr/components-contrib/state"
	jsoniter "github.com/json-iterator/go"
	"go.etcd.io/etcd/clientv3"
	"google.golang.org/grpc"
)

const defaultOperationTimeout = 10 * time.Second
const defaultSeparator = ","

var errMissingEndpoints = errors.New("endpoints are required")
var errInvalidDialTimeout = errors.New("DialTimeout is invalid")

// ETCD is a state store
type ETCD struct {
	json             jsoniter.API
	client           *clientv3.Client
	operationTimeout time.Duration
}

type configProperties struct {
	Endpoints        string `json:"endpoints"`
	DialTimeout      string `json:"dialTimeout"`
	OperationTimeout string `json:"operationTimeout"`
}

//--- StateStore ---

// NewETCD returns a new ETCD state store
func NewETCD() *ETCD {
	return &ETCD{
		json: jsoniter.ConfigFastest,
	}
}

// Init does metadata and connection parsing
func (r *ETCD) Init(metadata state.Metadata) error {
	cp, err := toConfigProperties(metadata.Properties)
	if err != nil {
		return err
	}
	err = validateRequired(cp)
	if err != nil {
		return err
	}

	clientConfig, err := toEtcdConfig(cp)
	if err != nil {
		return err
	}

	client, err := clientv3.New(*clientConfig)
	if err != nil {
		return err
	}

	r.client = client

	ot := defaultOperationTimeout
	newOt, err := time.ParseDuration(cp.OperationTimeout)
	if err == nil {
		r.operationTimeout = newOt
	}
	r.operationTimeout = ot

	return nil
}

func toConfigProperties(properties map[string]string) (*configProperties, error) {
	b, err := json.Marshal(properties)
	if err != nil {
		return nil, err
	}

	var configProps configProperties
	err = json.Unmarshal(b, &configProps)
	if err != nil {
		return nil, err
	}

	return &configProps, nil
}

func toEtcdConfig(configProps *configProperties) (*clientv3.Config, error) {
	endpoints := strings.Split(configProps.Endpoints, defaultSeparator)
	dialTimeout, err := time.ParseDuration(configProps.DialTimeout)
	if err != nil {
		return nil, err
	}

	clientConfig := clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
		DialOptions: []grpc.DialOption{grpc.WithBlock()},
	}

	return &clientConfig, nil
}

func validateRequired(configProps *configProperties) error {
	if len(configProps.Endpoints) == 0 {
		return errMissingEndpoints
	}

	_, err := time.ParseDuration(configProps.DialTimeout)
	if err != nil {
		return errInvalidDialTimeout
	}

	return nil
}

// Get retrieves state from ETCD with a key
func (r *ETCD) Get(req *state.GetRequest) (*state.GetResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.operationTimeout)
	defer cancel()
	resp, err := r.client.Get(ctx, req.Key, clientv3.WithSort(clientv3.SortByVersion, clientv3.SortDescend))
	if err != nil {
		return nil, err
	}

	if resp.Count == 0 {
		return &state.GetResponse{}, nil
	}

	return &state.GetResponse{
		Data: resp.Kvs[0].Value,
		ETag: fmt.Sprintf("%d", resp.Kvs[0].Version),
	}, nil
}

// Delete performs a delete operation
func (r *ETCD) Delete(req *state.DeleteRequest) error {
	ctx, cancelFn := context.WithTimeout(context.Background(), r.operationTimeout)
	defer cancelFn()
	_, err := r.client.Delete(ctx, req.Key)
	if err != nil {
		return err
	}

	return nil
}

// BulkDelete performs a bulk delete operation
func (r *ETCD) BulkDelete(req []state.DeleteRequest) error {
	for _, re := range req {
		err := r.Delete(&re)
		if err != nil {
			return err
		}
	}

	return nil
}

// Set saves state into ETCD
func (r *ETCD) Set(req *state.SetRequest) error {
	ctx, cancelFn := context.WithTimeout(context.Background(), r.operationTimeout)
	defer cancelFn()
	var vStr string
	b, ok := req.Value.([]byte)
	if ok {
		vStr = string(b)
	} else {
		vStr, _ = r.json.MarshalToString(req.Value)
	}

	_, err := r.client.Put(ctx, req.Key, vStr)
	if err != nil {
		return err
	}
	return nil
}

// BulkSet performs a bulks save operation
func (r *ETCD) BulkSet(req []state.SetRequest) error {
	for _, s := range req {
		err := r.Set(&s)
		if err != nil {
			return err
		}
	}

	return nil
}

// Watch watches on a key or prefix.
// The watched events will be returned through the returned channel.
func (r *ETCD) Watch(req *state.WatchStateRequest) (<-chan *state.Event, context.CancelFunc, error) {
	opts := []clientv3.OpOption{clientv3.WithProgressNotify()}

	if req.ETag != "" {
		rev, err := strconv.Atoi(req.ETag)
		if err != nil {
			return nil, nil, err
		}
		opts = append(opts, clientv3.WithRev(int64(rev)))
	}

	if _, exists := req.Metadata[state.WatchDiscardCreate]; !exists {
		opts = append(opts, clientv3.WithCreatedNotify())
	}
	if _, exists := req.Metadata[state.WatchDiscardModify]; exists {
		opts = append(opts, clientv3.WithFilterPut())
	}
	if _, exists := req.Metadata[state.WatchDiscardDelete]; exists {
		opts = append(opts, clientv3.WithFilterDelete())
	}
	if _, exists := req.Metadata[state.WatchFromKey]; exists {
		opts = append(opts, clientv3.WithFromKey())
	}
	if _, exists := req.Metadata[state.WatchMatchingPrefix]; exists {
		opts = append(opts, clientv3.WithPrefix())
	}
	if endKey, exists := req.Metadata[state.WatchInRange]; exists {
		opts = append(opts, clientv3.WithRange(endKey))
	}

	ctx, cancelFn := context.WithCancel(context.Background())
	c := r.client.Watch(ctx, req.Key, opts...)
	e := make(chan *state.Event)

	go func() {
		defer close(e)

		for resp := range c {
			if resp.IsProgressNotify() {
				continue
			}

			for _, evt := range resp.Events {
				ty := state.MODIFIED

				if evt.IsCreate() {
					ty = state.CREATED
				} else if evt.Type == clientv3.EventTypeDelete {
					ty = state.DELETED
				}

				s := &state.Event{
					Type:  ty,
					Key:   string(evt.Kv.Key),
					Value: evt.Kv.Value,
					ETag:  fmt.Sprintf("%d", evt.Kv.Version),
				}

				select {
				case <-ctx.Done():
					return
				case e <- s:
				}
			}

			if resp.Err() != nil {
				break
			}
		}
	}()

	return e, cancelFn, nil
}
