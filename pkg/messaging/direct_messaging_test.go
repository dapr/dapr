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

package messaging

import (
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"

	nr "github.com/dapr/components-contrib/nameresolution"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
)

func newDirectMessaging() *directMessaging {
	return &directMessaging{}
}

func TestDestinationHeaders(t *testing.T) {
	t.Run("destination header present", func(t *testing.T) {
		appID := "test1"
		req := invokev1.NewInvokeMethodRequest("GET")
		req.WithMetadata(map[string][]string{})

		dm := newDirectMessaging()

		dm.addAppIDHeadersToMetadata(appID, req)

		md := req.Metadata()[invokev1.DestinationIDHeader]
		assert.Equal(t, appID, md.Values[0])

		md = req.Metadata()[invokev1.SourceIDHeader]
		assert.Equal(t, dm.appID, md.Values[0])
	})
}

func TestCallerAndCalleeHeaders(t *testing.T) {
	t.Run("caller and callee header present", func(t *testing.T) {
		callerAppID := "caller-app"
		calleeAppID := "callee-app"
		req := invokev1.NewInvokeMethodRequest("GET")
		req.WithMetadata(map[string][]string{})

		dm := newDirectMessaging()
		dm.addCallerAndCalleeAppIDHeaderToMetadata(callerAppID, calleeAppID, req)
		actualCallerAppID := req.Metadata()[invokev1.CallerIDHeader]
		actualCalleeAppID := req.Metadata()[invokev1.CalleeIDHeader]
		assert.Equal(t, callerAppID, actualCallerAppID.Values[0])
		assert.Equal(t, calleeAppID, actualCalleeAppID.Values[0])
	})
}

func TestForwardedHeaders(t *testing.T) {
	t.Run("forwarded headers present", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("GET")
		req.WithMetadata(map[string][]string{})

		dm := newDirectMessaging()
		dm.hostAddress = "1"
		dm.hostName = "2"

		dm.addForwardedHeadersToMetadata(req)

		md := req.Metadata()[fasthttp.HeaderXForwardedFor]
		assert.Equal(t, "1", md.Values[0])

		md = req.Metadata()[fasthttp.HeaderXForwardedHost]
		assert.Equal(t, "2", md.Values[0])

		md = req.Metadata()[fasthttp.HeaderForwarded]
		assert.Equal(t, "for=1;by=1;host=2", md.Values[0])
	})

	t.Run("forwarded headers get appended", func(t *testing.T) {
		req := invokev1.NewInvokeMethodRequest("GET")
		req.WithMetadata(map[string][]string{
			fasthttp.HeaderXForwardedFor:  {"originalXForwardedFor"},
			fasthttp.HeaderXForwardedHost: {"originalXForwardedHost"},
			fasthttp.HeaderForwarded:      {"originalForwarded"},
		})

		dm := newDirectMessaging()
		dm.hostAddress = "1"
		dm.hostName = "2"

		dm.addForwardedHeadersToMetadata(req)

		md := req.Metadata()[fasthttp.HeaderXForwardedFor]
		assert.Equal(t, "originalXForwardedFor", md.Values[0])
		assert.Equal(t, "1", md.Values[1])

		md = req.Metadata()[fasthttp.HeaderXForwardedHost]
		assert.Equal(t, "originalXForwardedHost", md.Values[0])
		assert.Equal(t, "2", md.Values[1])

		md = req.Metadata()[fasthttp.HeaderForwarded]
		assert.Equal(t, "originalForwarded", md.Values[0])
		assert.Equal(t, "for=1;by=1;host=2", md.Values[1])
	})
}

func TestKubernetesNamespace(t *testing.T) {
	t.Run("no namespace", func(t *testing.T) {
		appID := "app1"

		dm := newDirectMessaging()
		id, ns, err := dm.requestAppIDAndNamespace(appID)

		assert.NoError(t, err)
		assert.Empty(t, ns)
		assert.Equal(t, appID, id)
	})

	t.Run("with namespace", func(t *testing.T) {
		appID := "app1.ns1"

		dm := newDirectMessaging()
		id, ns, err := dm.requestAppIDAndNamespace(appID)

		assert.NoError(t, err)
		assert.Equal(t, "ns1", ns)
		assert.Equal(t, "app1", id)
	})

	t.Run("invalid namespace", func(t *testing.T) {
		appID := "app1.ns1.ns2"

		dm := newDirectMessaging()
		_, _, err := dm.requestAppIDAndNamespace(appID)

		assert.Error(t, err)
	})
}

type testResolver struct{}

func (r testResolver) Init(metadata nr.Metadata) error {
	// nop
	return nil
}

func (r testResolver) ResolveID(req nr.ResolveRequest) (string, error) {
	switch req.ID {
	case "okapp":
		return "12.34.56.78", nil
	case "emptyapp":
		return "", nil
	case "failapp":
		return "", errors.New("failed")
	default:
		panic("not a valid case")
	}
}

func Test_directMessaging_getRemoteApp(t *testing.T) {
	const defaultNamespace = "mynamespace"
	type args struct {
		appID string
	}
	tests := []struct {
		name    string
		args    args
		want    remoteApp
		wantErr bool
	}{
		{
			name: "app with no namespace",
			args: args{appID: "okapp"},
			want: remoteApp{id: "okapp", namespace: defaultNamespace, address: "12.34.56.78"},
		},
		{
			name: "app with namespace",
			args: args{appID: "okapp.ns2"},
			want: remoteApp{id: "okapp", namespace: "ns2", address: "12.34.56.78"},
		},
		{
			name:    "empty appID",
			args:    args{appID: ""},
			wantErr: true,
		},
		{
			name:    "invalid appID",
			args:    args{appID: "not.valid.appID"},
			wantErr: true,
		},
		{
			name: "app doesn't exist",
			args: args{appID: "emptyapp"},
			want: remoteApp{id: "emptyapp", namespace: defaultNamespace, address: ""},
		},
		{
			name:    "resolver errors",
			args:    args{appID: "failapp"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &directMessaging{
				grpcPort:  3500,
				namespace: defaultNamespace,
				resolver:  &testResolver{},
			}
			got, err := d.getRemoteApp(tt.args.appID)
			if (err != nil) != tt.wantErr {
				t.Errorf("directMessaging.getRemoteApp() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("directMessaging.getRemoteApp() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("resolver is nil", func(t *testing.T) {
		d := &directMessaging{}
		_, err := d.getRemoteApp("foo")
		_ = assert.Error(t, err) &&
			assert.ErrorContains(t, err, "name resolver not initialized")
	})
}
