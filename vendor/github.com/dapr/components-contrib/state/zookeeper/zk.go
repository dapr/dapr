// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package zookeeper

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/dapr/components-contrib/state"
	"github.com/hashicorp/go-multierror"
	jsoniter "github.com/json-iterator/go"
	"github.com/samuel/go-zookeeper/zk"
)

const defaultMaxBufferSize = 1024 * 1024
const defaultMaxConnBufferSize = 1024 * 1024
const defaultKeyPrefixPath = "/dapr"

const anyVersion = -1

var defaultAcl = zk.WorldACL(zk.PermAll)

var errMissingServers = errors.New("servers are required")
var errInvalidSessionTimeout = errors.New("sessionTimeout is invalid")

type properties struct {
	Servers           string `json:"servers"`
	SessionTimeout    string `json:"sessionTimeout"`
	MaxBufferSize     int    `json:"maxBufferSize"`
	MaxConnBufferSize int    `json:"maxConnBufferSize"`
	KeyPrefixPath     string `json:"keyPrefixPath"`
}

type config struct {
	servers           []string
	sessionTimeout    time.Duration
	maxBufferSize     int
	maxConnBufferSize int
	keyPrefixPath     string
}

func newConfig(metadata map[string]string) (c *config, err error) {
	var buf []byte

	if buf, err = jsoniter.ConfigFastest.Marshal(metadata); err != nil {
		return
	}

	var props properties
	if err = jsoniter.ConfigFastest.Unmarshal(buf, &props); err != nil {
		return
	}

	return props.parse()
}

func (props *properties) parse() (*config, error) {
	if len(props.Servers) == 0 {
		return nil, errMissingServers
	}
	servers := strings.Split(props.Servers, ",")

	sessionTimeout, err := time.ParseDuration(props.SessionTimeout)
	if err != nil {
		return nil, errInvalidSessionTimeout
	}

	maxBufferSize := defaultMaxBufferSize
	if props.MaxBufferSize > 0 {
		maxBufferSize = props.MaxBufferSize
	}

	maxConnBufferSize := defaultMaxConnBufferSize
	if props.MaxConnBufferSize > 0 {
		maxConnBufferSize = props.MaxConnBufferSize
	}

	keyPrefixPath := defaultKeyPrefixPath
	if keyPrefixPath != "" {
		keyPrefixPath = props.KeyPrefixPath
	}

	return &config{
		servers,
		sessionTimeout,
		maxBufferSize,
		maxConnBufferSize,
		keyPrefixPath,
	}, nil
}

type Conn interface {
	Create(path string, data []byte, flags int32, acl []zk.ACL) (string, error)

	Get(path string) ([]byte, *zk.Stat, error)

	Set(path string, data []byte, version int32) (*zk.Stat, error)

	Delete(path string, version int32) error

	Multi(ops ...interface{}) ([]zk.MultiResponse, error)

	ExistsW(path string) (bool, *zk.Stat, <-chan zk.Event, error)
}

//--- StateStore ---

// StateStore is a state store
type StateStore struct {
	*config
	conn Conn
}

var _ Conn = (*zk.Conn)(nil)
var _ state.Store = (*StateStore)(nil)
var _ state.StoreWatcher = (*StateStore)(nil)

// NewZookeeperStateStore returns a new Zookeeper state store
func NewZookeeperStateStore() *StateStore {
	return &StateStore{}
}

func (s *StateStore) Init(metadata state.Metadata) (err error) {
	var c *config

	if c, err = newConfig(metadata.Properties); err != nil {
		return
	}

	log.WithFields(log.Fields{
		"metadata": metadata,
		"config":   c,
	}).Debug("Connect")

	conn, _, err := zk.Connect(c.servers, c.sessionTimeout,
		zk.WithMaxBufferSize(c.maxBufferSize), zk.WithMaxConnBufferSize(c.maxConnBufferSize))
	if err != nil {
		return
	}

	s.config = c
	s.conn = conn

	return
}

// Get retrieves state from Zookeeper with a key
func (s *StateStore) Get(req *state.GetRequest) (*state.GetResponse, error) {
	path := s.prefixedKey(req.Key)

	value, stat, err := s.conn.Get(path)

	log.WithFields(log.Fields{
		"path":  path,
		"value": string(value),
		"err":   err,
	}).Debug("Get")

	if err != nil {
		return nil, err
	}

	return &state.GetResponse{
		Data: value,
		ETag: strconv.Itoa(int(stat.Version)),
	}, nil
}

// Delete performs a delete operation
func (s *StateStore) Delete(req *state.DeleteRequest) error {
	r, err := s.newDeleteRequest(req)
	if err != nil {
		return err
	}

	return state.DeleteWithRetries(func(req *state.DeleteRequest) error {
		err := s.conn.Delete(r.Path, r.Version)

		log.WithFields(log.Fields{
			"request": req,
			"err":     err,
		}).Debug("Delete")

		if err == zk.ErrNoNode {
			return nil
		}
		return err
	}, req)
}

// BulkDelete performs a bulk delete operation
func (s *StateStore) BulkDelete(reqs []state.DeleteRequest) error {
	ops := make([]interface{}, 0, len(reqs))

	for _, req := range reqs {
		req, err := s.newDeleteRequest(&req)
		if err != nil {
			return err
		}

		ops = append(ops, req)
	}

	res, err := s.conn.Multi(ops...)

	log.WithFields(log.Fields{
		"ops": multiOps(ops),
		"res": multiRes(res),
		"err": err,
	}).Debug("BulkDelete")

	if err != nil {
		return err
	}

	for _, res := range res {
		if res.Error != nil && res.Error != zk.ErrNoNode {
			err = multierror.Append(err, res.Error)
		}
	}

	return err
}

// Set saves state into Zookeeper
func (s *StateStore) Set(req *state.SetRequest) error {
	r, err := s.newSetDataRequest(req)
	if err != nil {
		return err
	}

	log.WithFields(log.Fields{
		"request": req,
	}).Debug("Set")

	return state.SetWithRetries(func(req *state.SetRequest) error {
		_, err = s.conn.Set(r.Path, r.Data, r.Version)

		if err == zk.ErrNoNode {
			c := s.newCreateRequest(r)
			_, err = s.conn.Create(c.Path, c.Data, c.Flags, c.Acl)
		}

		return err
	}, req)
}

// BulkSet performs a bulks save operation
func (s *StateStore) BulkSet(reqs []state.SetRequest) error {
	ops := make([]interface{}, 0, len(reqs))

	for _, req := range reqs {
		req, err := s.newSetDataRequest(&req)
		if err != nil {
			return err
		}
		ops = append(ops, req)
	}

	for {
		res, err := s.conn.Multi(ops...)

		log.WithFields(log.Fields{
			"ops": multiOps(ops),
			"res": multiRes(res),
			"err": err,
		}).Debug("BulkSet")

		var retry []interface{}

		if err == zk.ErrNoNode {
			for _, op := range ops {
				if req, ok := op.(*zk.SetDataRequest); ok {
					retry = append(retry, s.newCreateRequest(req))
				}
			}
		} else {
			for i, res := range res {
				if res.Error != nil {
					if res.Error == zk.ErrNoNode {
						if req, ok := ops[i].(*zk.SetDataRequest); ok {
							retry = append(retry, s.newCreateRequest(req))
							continue
						}
					}

					err = multierror.Append(err, res.Error)
				}
			}
		}

		if retry == nil {
			return err
		}

		ops = retry
	}
}

// Watch watches on a key or prefix.
// The watched events will be returned through the returned channel.
func (s *StateStore) Watch(req *state.WatchStateRequest) (<-chan *state.Event, context.CancelFunc, error) {
	path := s.prefixedKey(req.Key)

	_, discardCreate := req.Metadata[state.WatchDiscardCreate]
	_, discardModify := req.Metadata[state.WatchDiscardModify]
	_, discardDelete := req.Metadata[state.WatchDiscardDelete]
	_, discardChildren := req.Metadata[state.WatchDiscardChildren]

	log.WithFields(log.Fields{
		"request":          req,
		"discard-create":   discardCreate,
		"discard-modify":   discardModify,
		"discard-delete":   discardDelete,
		"discard-children": discardChildren,
	}).Debug("Watch")

	ctx, cancelFn := context.WithCancel(context.Background())
	ch := make(chan *state.Event)

	go func() {
		defer close(ch)

		for ctx.Err() == nil {
			_, _, events, err := s.conn.ExistsW(path)

			if err != nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case evt := <-events:
				go func() {
					var ty state.EventType
					var value []byte
					var etag string

					switch evt.Type {
					case zk.EventNodeCreated:
						if discardCreate {
							return
						}
						ty = state.CREATED

					case zk.EventNodeDataChanged:
						if discardModify {
							return
						}
						ty = state.MODIFIED

					case zk.EventNodeDeleted:
						if discardDelete {
							return
						}
						ty = state.DELETED

					case zk.EventNodeChildrenChanged:
						if discardChildren {
							return
						}
						ty = state.CHILDREN
					default:
						return
					}

					if evt.Type == zk.EventNodeCreated || evt.Type == zk.EventNodeDataChanged {
						var stat *zk.Stat
						var err error

						value, stat, err = s.conn.Get(s.prefixedKey(req.Key))

						if err == nil {
							etag = strconv.Itoa(int(stat.Version))
						}
					}

					e := &state.Event{
						Type:  ty,
						Key:   evt.Path,
						Value: value,
						ETag:  etag,
					}

					select {
					case <-ctx.Done():
						return
					case ch <- e:
					}
				}()
			}
		}
	}()

	return ch, cancelFn, nil
}

func (s *StateStore) newCreateRequest(req *zk.SetDataRequest) *zk.CreateRequest {
	return &zk.CreateRequest{Path: req.Path, Data: req.Data, Acl: defaultAcl}
}

func (s *StateStore) newDeleteRequest(req *state.DeleteRequest) (*zk.DeleteRequest, error) {
	err := state.CheckDeleteRequestOptions(req)
	if err != nil {
		return nil, err
	}

	var version int32

	if req.Options.Concurrency == state.LastWrite {
		version = anyVersion
	} else {
		version = s.parseETag(req.ETag)
	}

	return &zk.DeleteRequest{
		Path:    s.prefixedKey(req.Key),
		Version: version,
	}, nil
}

func (s *StateStore) newSetDataRequest(req *state.SetRequest) (*zk.SetDataRequest, error) {
	err := state.CheckSetRequestOptions(req)
	if err != nil {
		return nil, err
	}

	data, err := s.marshalData(req.Value)
	if err != nil {
		return nil, err
	}

	var version int32

	if req.Options.Concurrency == state.LastWrite {
		version = anyVersion
	} else {
		version = s.parseETag(req.ETag)
	}

	return &zk.SetDataRequest{
		Path:    s.prefixedKey(req.Key),
		Data:    data,
		Version: version,
	}, nil
}

func (s *StateStore) prefixedKey(key string) string {
	if s.config == nil {
		return key
	}

	return path.Join(s.keyPrefixPath, key)
}

func (s *StateStore) parseETag(etag string) int32 {
	if etag != "" {
		version, err := strconv.Atoi(etag)
		if err == nil {
			return int32(version)
		}
	}
	return anyVersion
}

func (s *StateStore) marshalData(v interface{}) ([]byte, error) {
	if buf, ok := v.([]byte); ok {
		return buf, nil
	}

	return jsoniter.ConfigFastest.Marshal(v)
}

type multiOps []interface{}

func (ops multiOps) String() string {
	var msgs []string
	for _, op := range ops {
		switch op := op.(type) {
		case *zk.SetDataRequest:
			msgs = append(msgs, fmt.Sprintf("Set(%s@%d = %v)", op.Path, op.Version, string(op.Data)))
		case *zk.CreateRequest:
			msgs = append(msgs, fmt.Sprintf("Create(%s = %v)", op.Path, string(op.Data)))
		case *zk.DeleteRequest:
			msgs = append(msgs, fmt.Sprintf("Delete(%s@%d)", op.Path, op.Version))
		case *zk.CheckVersionRequest:
			msgs = append(msgs, fmt.Sprintf("CheckVersion(%s@%d)", op.Path, op.Version))
		}
	}
	return "[" + strings.Join(msgs, ", ") + "]"
}

type multiRes []zk.MultiResponse

func (res multiRes) String() string {
	var msgs []string
	for _, r := range res {
		msgs = append(msgs, r.String)
	}
	return "[" + strings.Join(msgs, ", ") + "]"
}
