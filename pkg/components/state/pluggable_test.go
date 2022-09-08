/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package state

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/components-contrib/state/query"
	"github.com/dapr/dapr/pkg/components"
	v1 "github.com/dapr/dapr/pkg/proto/common/v1"
	proto "github.com/dapr/dapr/pkg/proto/components/v1"
	"github.com/dapr/kit/logger"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const bufSize = 1024 * 1024

type server struct {
	proto.UnimplementedStateStoreServer
	proto.UnimplementedTransactionalStateStoreServer
	deleteCalled       atomic.Int64
	onDeleteCalled     func(*proto.DeleteRequest)
	deleteErr          error
	getCalled          atomic.Int64
	onGetCalled        func(*proto.GetRequest)
	getErr             error
	getResponse        *proto.GetResponse
	setCalled          atomic.Int64
	onSetCalled        func(*proto.SetRequest)
	setErr             error
	pingCalled         atomic.Int64
	pingErr            error
	bulkDeleteCalled   atomic.Int64
	onBulkDeleteCalled func(*proto.BulkDeleteRequest)
	bulkDeleteErr      error
	bulkGetCalled      atomic.Int64
	onBulkGetCalled    func(*proto.BulkGetRequest)
	bulkGetErr         error
	bulkGetResponse    *proto.BulkGetResponse
	bulkSetCalled      atomic.Int64
	onBulkSetCalled    func(*proto.BulkSetRequest)
	bulkSetErr         error
	transactCalled     atomic.Int64
	onTransactCalled   func(*proto.TransactionalStateRequest)
	transactErr        error
	queryCalled        atomic.Int64
	onQueryCalled      func(*proto.QueryRequest)
	queryResp          *proto.QueryResponse
	queryErr           error
}

func (s *server) Query(_ context.Context, req *proto.QueryRequest) (*proto.QueryResponse, error) {
	s.queryCalled.Add(1)
	if s.onQueryCalled != nil {
		s.onQueryCalled(req)
	}
	return s.queryResp, s.queryErr
}

func (s *server) Transact(_ context.Context, req *proto.TransactionalStateRequest) (*proto.TransactionalStateResponse, error) {
	s.transactCalled.Add(1)
	if s.onTransactCalled != nil {
		s.onTransactCalled(req)
	}
	return &proto.TransactionalStateResponse{}, s.transactErr
}

func (s *server) Delete(ctx context.Context, req *proto.DeleteRequest) (*proto.DeleteResponse, error) {
	s.deleteCalled.Add(1)
	if s.onDeleteCalled != nil {
		s.onDeleteCalled(req)
	}
	return &proto.DeleteResponse{}, s.deleteErr
}

func (s *server) Get(ctx context.Context, req *proto.GetRequest) (*proto.GetResponse, error) {
	s.getCalled.Add(1)
	if s.onGetCalled != nil {
		s.onGetCalled(req)
	}
	return s.getResponse, s.getErr
}

func (s *server) Set(ctx context.Context, req *proto.SetRequest) (*proto.SetResponse, error) {
	s.setCalled.Add(1)
	if s.onSetCalled != nil {
		s.onSetCalled(req)
	}
	return &proto.SetResponse{}, s.setErr
}

func (s *server) Ping(context.Context, *proto.PingRequest) (*proto.PingResponse, error) {
	s.pingCalled.Add(1)
	return &proto.PingResponse{}, s.pingErr
}

func (s *server) BulkDelete(ctx context.Context, req *proto.BulkDeleteRequest) (*proto.BulkDeleteResponse, error) {
	s.bulkDeleteCalled.Add(1)
	if s.onBulkDeleteCalled != nil {
		s.onBulkDeleteCalled(req)
	}
	return &proto.BulkDeleteResponse{}, s.bulkDeleteErr
}

func (s *server) BulkGet(ctx context.Context, req *proto.BulkGetRequest) (*proto.BulkGetResponse, error) {
	s.bulkGetCalled.Add(1)
	if s.onBulkGetCalled != nil {
		s.onBulkGetCalled(req)
	}
	return s.bulkGetResponse, s.bulkGetErr
}

func (s *server) BulkSet(ctx context.Context, req *proto.BulkSetRequest) (*proto.BulkSetResponse, error) {
	s.bulkSetCalled.Add(1)
	if s.onBulkSetCalled != nil {
		s.onBulkSetCalled(req)
	}
	return &proto.BulkSetResponse{}, s.bulkSetErr
}

func (s *server) Init(context.Context, *proto.InitRequest) (*proto.InitResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Init not implemented")
}

var testLogger = logger.NewLogger("state-pluggable-logger")

// getStateStore returns a state store connected to the given server
func getStateStore(srv *server) (stStore *grpcStateStore, cleanup func(), err error) {
	lis := bufconn.Listen(bufSize)
	s := grpc.NewServer()
	proto.RegisterStateStoreServer(s, srv)
	proto.RegisterTransactionalStateStoreServer(s, srv)
	proto.RegisterQueriableStateStoreServer(s, srv)
	go func() {
		if serveErr := s.Serve(lis); serveErr != nil {
			testLogger.Debugf("Server exited with error: %v", serveErr)
		}
	}()
	ctx := context.Background()
	conn, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}

	client := newStateStoreClient(conn)
	stStore = fromPluggable(testLogger, components.Pluggable{})
	stStore.Client = client
	return stStore, func() {
		lis.Close()
		conn.Close()
	}, nil
}

// wrapString into quotes
func wrapString(str string) string {
	return fmt.Sprintf("\"%s\"", str)
}

func TestComponentCalls(t *testing.T) {
	t.Run("features should return the component features'", func(t *testing.T) {
		stStore, cleanup, err := getStateStore(&server{})
		require.NoError(t, err)
		defer cleanup()
		assert.Empty(t, stStore.Features())
		stStore.features = []state.Feature{state.FeatureETag}
		assert.NotEmpty(t, stStore.Features())
		assert.Equal(t, stStore.Features()[0], state.FeatureETag)
	})

	t.Run("delete should call delete grpc method", func(t *testing.T) {
		const fakeKey = "fakeKey"

		svc := &server{
			onDeleteCalled: func(req *proto.DeleteRequest) {
				assert.Equal(t, req.Key, fakeKey)
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()
		err = stStore.Delete(&state.DeleteRequest{
			Key: fakeKey,
		})

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.deleteCalled.Load())
	})

	t.Run("delete should return an err when grpc delete returns an error", func(t *testing.T) {
		const fakeKey = "fakeKey"
		fakeErr := errors.New("my-fake-err")

		svc := &server{
			onDeleteCalled: func(req *proto.DeleteRequest) {
				assert.Equal(t, req.Key, fakeKey)
			},
			deleteErr: fakeErr,
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()
		err = stStore.Delete(&state.DeleteRequest{
			Key: fakeKey,
		})

		assert.NotNil(t, err)
		assert.Equal(t, int64(1), svc.deleteCalled.Load())
	})

	t.Run("get should return an err when grpc get returns an error", func(t *testing.T) {
		const fakeKey = "fakeKey"

		svc := &server{
			onGetCalled: func(req *proto.GetRequest) {
				assert.Equal(t, req.Key, fakeKey)
			},
			getErr: errors.New("my-fake-err"),
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		resp, err := stStore.Get(&state.GetRequest{
			Key: fakeKey,
		})

		assert.NotNil(t, err)
		assert.Equal(t, int64(1), svc.getCalled.Load())
		assert.Nil(t, resp)
	})

	t.Run("get should return an err when response is nil", func(t *testing.T) {
		const fakeKey = "fakeKey"

		svc := &server{
			onGetCalled: func(req *proto.GetRequest) {
				assert.Equal(t, req.Key, fakeKey)
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		resp, err := stStore.Get(&state.GetRequest{
			Key: fakeKey,
		})

		assert.NotNil(t, err)
		assert.Equal(t, int64(1), svc.getCalled.Load())
		assert.Nil(t, resp)
	})

	t.Run("get should return get response when response is returned from the grpc call", func(t *testing.T) {
		const fakeKey = "fakeKey"
		fakeData := []byte(`fake-data`)

		svc := &server{
			onGetCalled: func(req *proto.GetRequest) {
				assert.Equal(t, req.Key, fakeKey)
			},
			getResponse: &proto.GetResponse{
				Data: fakeData,
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		resp, err := stStore.Get(&state.GetRequest{
			Key: fakeKey,
		})

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.getCalled.Load())
		assert.Equal(t, resp.Data, fakeData)
	})

	t.Run("set should return an err when grpc set returns it", func(t *testing.T) {
		const fakeKey, fakeData = "fakeKey", "fakeData"

		svc := &server{
			onSetCalled: func(req *proto.SetRequest) {
				assert.Equal(t, req.Key, fakeKey)
				assert.Equal(t, req.Value, []byte(wrapString(fakeData)))
			},
			setErr: errors.New("fake-set-err"),
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.Set(&state.SetRequest{
			Key:   fakeKey,
			Value: fakeData,
		})

		assert.NotNil(t, err)
		assert.Equal(t, int64(1), svc.setCalled.Load())
	})

	t.Run("set should not return an err when grpc not returns an error", func(t *testing.T) {
		const fakeKey, fakeData = "fakeKey", "fakeData"

		svc := &server{
			onSetCalled: func(req *proto.SetRequest) {
				assert.Equal(t, req.Key, fakeKey)
				assert.Equal(t, req.Value, []byte(wrapString(fakeData)))
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.Set(&state.SetRequest{
			Key:   fakeKey,
			Value: fakeData,
		})

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.setCalled.Load())
	})

	t.Run("ping should not return an err when grpc not returns an error", func(t *testing.T) {
		svc := &server{}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.Ping()

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.pingCalled.Load())
	})

	t.Run("ping should return an err when grpc returns an error", func(t *testing.T) {
		svc := &server{
			pingErr: errors.New("fake-err"),
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.Ping()

		assert.NotNil(t, err)
		assert.Equal(t, int64(1), svc.pingCalled.Load())
	})

	t.Run("bulkSet should return an err when grpc returns an error", func(t *testing.T) {
		svc := &server{
			bulkSetErr: errors.New("fake-bulk-err"),
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.BulkSet([]state.SetRequest{})

		assert.NotNil(t, err)
		assert.Equal(t, int64(1), svc.bulkSetCalled.Load())
	})

	t.Run("bulkSet should returns an error when attempted to set value to nil", func(t *testing.T) {
		requests := []state.SetRequest{
			{
				Key: "key-1",
			},
		}
		svc := &server{
			onBulkSetCalled: func(_ *proto.BulkSetRequest) {
				assert.FailNow(t, "bulkset should not be called")
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.BulkSet(requests)

		assert.ErrorIs(t, ErrNilSetValue, err)
		assert.Equal(t, int64(0), svc.bulkSetCalled.Load())
	})

	t.Run("bulkSet should send a bulkSetRequest containing all setRequest items", func(t *testing.T) {
		const fakeKey, otherFakeKey, fakeData = "fakeKey", "otherFakeKey", "fakeData"
		requests := []state.SetRequest{
			{
				Key:   fakeKey,
				Value: fakeData,
			},
			{
				Key:   otherFakeKey,
				Value: fakeData,
			},
		}
		svc := &server{
			onBulkSetCalled: func(bsr *proto.BulkSetRequest) {
				assert.Len(t, bsr.Items, len(requests))
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.BulkSet(requests)

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.bulkSetCalled.Load())
	})

	t.Run("bulkDelete should send a bulkDeleteRequest containing all deleted items", func(t *testing.T) {
		const fakeKey, otherFakeKey = "fakeKey", "otherFakeKey"
		requests := []state.DeleteRequest{
			{
				Key: fakeKey,
			},
			{
				Key: otherFakeKey,
			},
		}
		svc := &server{
			onBulkDeleteCalled: func(bsr *proto.BulkDeleteRequest) {
				assert.Len(t, bsr.Items, len(requests))
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.BulkDelete(requests)

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.bulkDeleteCalled.Load())
	})

	t.Run("bulkDelete should return an error when grpc bulkDelete returns an error", func(t *testing.T) {
		requests := []state.DeleteRequest{
			{
				Key: "fake",
			},
		}
		svc := &server{
			bulkDeleteErr: errors.New("fake-bulk-delete-err"),
			onBulkDeleteCalled: func(bsr *proto.BulkDeleteRequest) {
				assert.Len(t, bsr.Items, len(requests))
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.BulkDelete(requests)

		assert.NotNil(t, err)
		assert.Equal(t, int64(1), svc.bulkDeleteCalled.Load())
	})

	t.Run("bulkGet should return an error when grpc bulkGet returns an error", func(t *testing.T) {
		requests := []state.GetRequest{
			{
				Key: "fake",
			},
		}
		svc := &server{
			bulkGetErr: errors.New("fake-bulk-get-err"),
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		got, resp, err := stStore.BulkGet(requests)

		assert.NotNil(t, err)
		assert.False(t, got)
		assert.Nil(t, resp)
		assert.Equal(t, int64(1), svc.bulkGetCalled.Load())
	})

	t.Run("bulkGet should send a bulkGetRequest containing all retrieved items", func(t *testing.T) {
		const fakeKey, otherFakeKey = "fakeKey", "otherFakeKey"
		requests := []state.GetRequest{
			{
				Key: fakeKey,
			},
			{
				Key: otherFakeKey,
			},
		}
		respItems := []*proto.BulkStateItem{{
			Key: fakeKey,
		}, {Key: otherFakeKey}}

		const gotValue = false
		svc := &server{
			onBulkGetCalled: func(bsr *proto.BulkGetRequest) {
				assert.Len(t, bsr.Items, len(requests))
			},
			bulkGetResponse: &proto.BulkGetResponse{
				Items: respItems,
				Got:   gotValue,
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		got, resp, err := stStore.BulkGet(requests)

		require.NoError(t, err)
		assert.Equal(t, got, gotValue)
		assert.NotNil(t, resp)
		assert.Len(t, resp, len(requests))
		assert.Equal(t, int64(1), svc.bulkGetCalled.Load())
	})

	t.Run("transact should returns an error when grpc returns an error", func(t *testing.T) {
		svc := &server{
			transactErr: errors.New("transact-fake-err"),
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.Multi(&state.TransactionalStateRequest{
			Operations: []state.TransactionalStateOperation{},
			Metadata:   map[string]string{},
		})

		assert.NotNil(t, err)
		assert.Equal(t, int64(1), svc.transactCalled.Load())
	})

	t.Run("transact should send a transact containing all operations", func(t *testing.T) {
		const fakeKey, otherFakeKey, fakeData = "fakeKey", "otherFakeKey", "fakeData"
		operations := []state.SetRequest{
			{
				Key:   fakeKey,
				Value: fakeData,
			},
			{
				Key:   otherFakeKey,
				Value: fakeData,
			},
		}
		svc := &server{
			onTransactCalled: func(bsr *proto.TransactionalStateRequest) {
				assert.Len(t, bsr.Operations, len(operations))
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		err = stStore.Multi(&state.TransactionalStateRequest{
			Operations: []state.TransactionalStateOperation{
				{
					Request: operations[0],
				},
				{
					Request: operations[1],
				},
			},
		})

		require.NoError(t, err)
		assert.Equal(t, int64(1), svc.transactCalled.Load())
	})

	t.Run("query should return an error when grpc query returns an error", func(t *testing.T) {
		svc := &server{
			queryErr: errors.New("fake-query-err"),
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		resp, err := stStore.Query(&state.QueryRequest{})

		assert.NotNil(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, int64(1), svc.queryCalled.Load())
	})

	t.Run("query should send a QueryRequest containing all filters", func(t *testing.T) {
		filters := map[string]interface{}{
			"a": []string{"a"},
		}
		request := &state.QueryRequest{
			Query: query.Query{
				Filters: filters,
			},
			Metadata: map[string]string{},
		}
		results := []*proto.QueryItem{
			{
				Key:         "",
				Data:        []byte{},
				Etag:        &v1.Etag{},
				Error:       "",
				ContentType: "",
			},
		}
		svc := &server{
			onQueryCalled: func(bsr *proto.QueryRequest) {
				assert.Len(t, bsr.Query.Filter, len(filters))
			},
			queryResp: &proto.QueryResponse{
				Items: results,
			},
		}
		stStore, cleanup, err := getStateStore(svc)
		require.NoError(t, err)
		defer cleanup()

		resp, err := stStore.Query(request)

		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Results, len(results))
		assert.Equal(t, int64(1), svc.queryCalled.Load())
	})
}

//nolint:nosnakecase
func TestMappers(t *testing.T) {
	t.Run("consistencyOf should return unspecified for unknown consistency", func(t *testing.T) {
		assert.Equal(t, v1.StateOptions_CONSISTENCY_UNSPECIFIED, consistencyOf(""))
	})

	t.Run("consistencyOf should return proper consistency when well-known consistency is used", func(t *testing.T) {
		assert.Equal(t, v1.StateOptions_CONSISTENCY_EVENTUAL, consistencyOf(state.Eventual))
		assert.Equal(t, v1.StateOptions_CONSISTENCY_STRONG, consistencyOf(state.Strong))
	})

	t.Run("concurrencyOf should return unspecified for unknown concurrency", func(t *testing.T) {
		assert.Equal(t, v1.StateOptions_CONCURRENCY_UNSPECIFIED, concurrencyOf(""))
	})

	t.Run("concurrencyOf should return proper concurrency when well-known concurrency is used", func(t *testing.T) {
		assert.Equal(t, v1.StateOptions_CONCURRENCY_FIRST_WRITE, concurrencyOf(state.FirstWrite))
		assert.Equal(t, v1.StateOptions_CONCURRENCY_LAST_WRITE, concurrencyOf(state.LastWrite))
	})

	t.Run("toGetRequest should return nil when receiving a nil request", func(t *testing.T) {
		assert.Nil(t, toGetRequest(nil))
	})

	t.Run("toGetRequest should map all properties from the given request", func(t *testing.T) {
		const fakeKey = "fake"
		getRequest := toGetRequest(&state.GetRequest{
			Key: fakeKey,
			Metadata: map[string]string{
				fakeKey: fakeKey,
			},
			Options: state.GetStateOption{
				Consistency: state.Eventual,
			},
		})
		assert.Equal(t, getRequest.Key, fakeKey)
		assert.Equal(t, getRequest.Metadata[fakeKey], fakeKey)
		assert.Equal(t, getRequest.Consistency, v1.StateOptions_CONSISTENCY_EVENTUAL)
	})

	t.Run("fromGetResponse should map all properties from the given response", func(t *testing.T) {
		fakeData := []byte(`mydata`)
		fakeKey := "key"
		fakeETag := "etag"
		resp := fromGetResponse(&proto.GetResponse{
			Data: fakeData,
			Etag: &v1.Etag{
				Value: fakeETag,
			},
			Metadata: map[string]string{
				fakeKey: fakeKey,
			},
		})
		assert.Equal(t, resp.Data, fakeData)
		assert.Equal(t, resp.ETag, &fakeETag)
		assert.Equal(t, resp.Metadata[fakeKey], fakeKey)
	})

	t.Run("toETagRequest should return nil when receiving a nil etag", func(t *testing.T) {
		assert.Nil(t, toETagRequest(nil))
	})
	t.Run("toETagRequest should set the etag value when receiving a valid etag value", func(t *testing.T) {
		fakeETag := "this"
		etagRequest := toETagRequest(&fakeETag)
		assert.NotNil(t, etagRequest)
		assert.Equal(t, etagRequest.Value, fakeETag)
	})

	t.Run("fromETagResponse should return nil when receiving a nil etag response", func(t *testing.T) {
		assert.Nil(t, fromETagResponse(nil))
	})
	t.Run("fromETagResponse should return the etag value from the response", func(t *testing.T) {})

	t.Run("toDeleteRequest should return nil when receiving a nil delete request", func(t *testing.T) {
		assert.Nil(t, toDeleteRequest(nil))
	})
	t.Run("toDeleteRequest map all properties for the given request", func(t *testing.T) {})

	t.Run("toSetRequest should return nil when receiving a nil set request", func(t *testing.T) {
		req, err := toSetRequest(nil)
		require.NoError(t, err)
		assert.Nil(t, req)
	})

	t.Run("toSetRequest should wrap string into quotes", func(t *testing.T) {
		const fakeKey, fakePropValue = "fakeKey", "fakePropValue"
		fakeEtag := "fakeEtag"
		for _, fakeValue := range []any{"fakeStrValue", []byte(`fakeByteValue`), make(map[string]string)} {
			req, err := toSetRequest(&state.SetRequest{
				Key:   fakeKey,
				Value: fakeValue,
				ETag:  &fakeEtag,
				Metadata: map[string]string{
					fakeKey: fakePropValue,
				},
				Options: state.SetStateOption{
					Concurrency: state.LastWrite,
					Consistency: state.Eventual,
				},
			})
			require.NoError(t, err)
			assert.NotNil(t, req)
			assert.Equal(t, req.Key, fakeKey)
			assert.NotNil(t, req.Value)
			if v, ok := fakeValue.(string); ok {
				assert.Equal(t, string(req.Value), wrapString(v))
			}
			assert.Equal(t, req.Metadata[fakeKey], fakePropValue)
			assert.Equal(t, req.Options.Concurrency, v1.StateOptions_CONCURRENCY_LAST_WRITE)
			assert.Equal(t, req.Options.Consistency, v1.StateOptions_CONSISTENCY_EVENTUAL)
		}
	})

	t.Run("toSetRequest accept and parse values as []byte", func(t *testing.T) {
		const fakeKey, fakePropValue = "fakeKey", "fakePropValue"
		fakeEtag := "fakeEtag"
		for _, fakeValue := range []any{"fakeStrValue", []byte(`fakeByteValue`), make(map[string]string)} {
			req, err := toSetRequest(&state.SetRequest{
				Key:   fakeKey,
				Value: fakeValue,
				ETag:  &fakeEtag,
				Metadata: map[string]string{
					fakeKey: fakePropValue,
				},
				Options: state.SetStateOption{
					Concurrency: state.LastWrite,
					Consistency: state.Eventual,
				},
			})
			require.NoError(t, err)
			assert.NotNil(t, req)
			assert.Equal(t, req.Key, fakeKey)
			assert.NotNil(t, req.Value)
			assert.Equal(t, req.Metadata[fakeKey], fakePropValue)
			assert.Equal(t, req.Options.Concurrency, v1.StateOptions_CONCURRENCY_LAST_WRITE)
			assert.Equal(t, req.Options.Consistency, v1.StateOptions_CONSISTENCY_EVENTUAL)
		}

		t.Run("toTransact should return err when type is unrecognized", func(t *testing.T) {
			req, err := toTransactOperation(state.TransactionalStateOperation{
				Request: make(map[struct{}]struct{}),
			})
			assert.Nil(t, req)
			assert.ErrorIs(t, err, ErrTransactOperationNotSupported)
		})

		t.Run("toTransact should return set operation when type is SetOperation", func(t *testing.T) {
			const fakeData = "fakeData"
			req, err := toTransactOperation(state.TransactionalStateOperation{
				Request: state.SetRequest{
					Key:   fakeKey,
					Value: fakeData,
				},
			})
			require.NoError(t, err)
			assert.NotNil(t, req)
			assert.IsType(t, &proto.TransactionalStateOperation_Set{}, req.Request)
		})

		t.Run("toTransact should return delete operation when type is SetOperation", func(t *testing.T) {
			req, err := toTransactOperation(state.TransactionalStateOperation{
				Request: state.DeleteRequest{
					Key: fakeKey,
				},
			})
			require.NoError(t, err)
			assert.NotNil(t, req)
			assert.IsType(t, &proto.TransactionalStateOperation_Delete{}, req.Request)
		})
	})
}
