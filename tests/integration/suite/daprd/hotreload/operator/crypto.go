/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package operator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/operator"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(crypto))
}

type crypto struct {
	daprd    *daprd.Daprd
	operator *operator.Operator

	cryptoDir1     string
	cryptoDir2     string
	cryptoDir1JSON common.DynamicValue
	cryptoDir2JSON common.DynamicValue
}

func (c *crypto) Setup(t *testing.T) []framework.Option {
	sentry := sentry.New(t)

	c.operator = operator.New(t,
		operator.WithSentry(sentry),
		operator.WithGetConfigurationFn(func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error) {
			return &operatorv1.GetConfigurationResponse{
				Configuration: []byte(
					`{"kind":"Configuration","apiVersion":"dapr.io/v1alpha1","metadata":{"name":"hotreloading"},"spec":{"features":[{"name":"HotReload","enabled":true}]}}`,
				),
			}, nil
		}),
	)

	c.cryptoDir1, c.cryptoDir2 = t.TempDir(), t.TempDir()

	dir1J, err := json.Marshal(c.cryptoDir1)
	require.NoError(t, err)
	dir2J, err := json.Marshal(c.cryptoDir2)
	require.NoError(t, err)
	c.cryptoDir1JSON = common.DynamicValue{JSON: apiextv1.JSON{Raw: dir1J}}
	c.cryptoDir2JSON = common.DynamicValue{JSON: apiextv1.JSON{Raw: dir2J}}

	c.daprd = daprd.New(t,
		daprd.WithMode("kubernetes"),
		daprd.WithConfigs("hotreloading"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(sentry.Address()),
		daprd.WithControlPlaneAddress(c.operator.Address(t)),
		daprd.WithDisableK8sSecretStore(true),
	)

	return []framework.Option{
		framework.WithProcesses(sentry, c.operator, c.daprd),
	}
}

func (c *crypto) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	client := c.daprd.GRPCClient(t, ctx)

	t.Run("expect no components to be loaded yet", func(t *testing.T) {
		resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		assert.Empty(t, resp.GetRegisteredComponents())
		c.encryptDecryptFail(t, ctx, client, "crypto1")
		c.encryptDecryptFail(t, ctx, client, "crypto2")
	})

	t.Run("creating crypto component should make it available", func(t *testing.T) {
		pk := make([]byte, 32)
		for i := range pk {
			pk[i] = byte(i)
		}

		require.NoError(t, os.WriteFile(filepath.Join(c.cryptoDir1, "crypto1"), pk, 0o600))
		newComp := compapi.Component{
			TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
			ObjectMeta: metav1.ObjectMeta{Name: "crypto1"},
			Spec: compapi.ComponentSpec{
				Type:     "crypto.dapr.localstorage",
				Version:  "v1",
				Metadata: []common.NameValuePair{{Name: "path", Value: c.cryptoDir1JSON}},
			},
		}
		c.operator.SetComponents(newComp)
		c.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			assert.NoError(c, err)
			assert.Len(c, resp.GetRegisteredComponents(), 1)
		}, time.Second*10, time.Millisecond*10)

		c.encryptDecrypt(t, ctx, client, "crypto1")
		c.encryptDecryptFail(t, ctx, client, "crypto2")
		c.encryptDecryptFail(t, ctx, client, "crypto3")
	})

	t.Run("creating another crypto component should make it available", func(t *testing.T) {
		pk := make([]byte, 32)
		for i := range pk {
			pk[i] = byte(i + 32)
		}
		require.NoError(t, os.WriteFile(filepath.Join(c.cryptoDir2, "crypto2"), pk, 0o600))

		newComp := compapi.Component{
			TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
			ObjectMeta: metav1.ObjectMeta{Name: "crypto2"},
			Spec: compapi.ComponentSpec{
				Type:     "crypto.dapr.localstorage",
				Version:  "v1",
				Metadata: []common.NameValuePair{{Name: "path", Value: c.cryptoDir2JSON}},
			},
		}
		c.operator.AddComponents(newComp)
		c.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			assert.NoError(c, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*10, time.Millisecond*10)

		c.encryptDecrypt(t, ctx, client, "crypto1")
		c.encryptDecrypt(t, ctx, client, "crypto2")
		c.encryptDecryptFail(t, ctx, client, "crypto3")
	})

	t.Run("creating third crypto component should make it available", func(t *testing.T) {
		pk := make([]byte, 32)
		for i := range pk {
			pk[i] = byte(i + 32)
		}
		require.NoError(t, os.WriteFile(filepath.Join(c.cryptoDir2, "crypto3"), pk, 0o600))

		newComp := compapi.Component{
			TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
			ObjectMeta: metav1.ObjectMeta{Name: "crypto3"},
			Spec: compapi.ComponentSpec{
				Type:     "crypto.dapr.localstorage",
				Version:  "v1",
				Metadata: []common.NameValuePair{{Name: "path", Value: c.cryptoDir2JSON}},
			},
		}
		c.operator.AddComponents(newComp)
		c.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			assert.NoError(c, err)
			assert.Len(c, resp.GetRegisteredComponents(), 3)
		}, time.Second*10, time.Millisecond*10)

		c.encryptDecrypt(t, ctx, client, "crypto1")
		c.encryptDecrypt(t, ctx, client, "crypto2")
		c.encryptDecrypt(t, ctx, client, "crypto3")
	})

	t.Run("deleting crypto component (through type update) should make it no longer available", func(t *testing.T) {
		updateComp := compapi.Component{
			TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
			ObjectMeta: metav1.ObjectMeta{Name: "crypto2"},
			Spec: compapi.ComponentSpec{
				Type:    "state.in-memory",
				Version: "v1",
			},
		}
		c.operator.SetComponents(c.operator.Components()[0], updateComp, c.operator.Components()[2])
		c.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &updateComp, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			assert.NoError(c, err)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{Name: "crypto1", Type: "crypto.dapr.localstorage", Version: "v1"},
				{Name: "crypto3", Type: "crypto.dapr.localstorage", Version: "v1"},
				{
					Name: "crypto2", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp.GetRegisteredComponents())
		}, time.Second*10, time.Millisecond*10)

		c.encryptDecrypt(t, ctx, client, "crypto1")
		c.encryptDecryptFail(t, ctx, client, "crypto2")
		c.encryptDecrypt(t, ctx, client, "crypto3")
	})

	t.Run("deleting all crypto components should make them no longer available", func(t *testing.T) {
		delComp := c.operator.Components()[0]
		c.operator.SetComponents(c.operator.Components()[1], c.operator.Components()[2])
		c.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &delComp, EventType: operatorv1.ResourceEventType_DELETED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			assert.NoError(c, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*10, time.Millisecond*10)

		delComp = c.operator.Components()[0]
		c.operator.SetComponents(c.operator.Components()[1])
		c.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &delComp, EventType: operatorv1.ResourceEventType_DELETED})

		updateComp := c.operator.Components()[0]
		updateComp.Spec.Type = "state.in-memory"
		updateComp.Spec.Metadata = nil
		c.operator.SetComponents(updateComp)
		assert.Equal(t, "crypto3", c.operator.Components()[0].Name)
		c.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &updateComp, EventType: operatorv1.ResourceEventType_UPDATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			assert.NoError(c, err)
			assert.ElementsMatch(c, []*rtv1.RegisteredComponents{
				{
					Name: "crypto3", Type: "state.in-memory", Version: "v1",
					Capabilities: []string{"ETAG", "TRANSACTIONAL", "TTL", "DELETE_WITH_PREFIX", "ACTOR"},
				},
			}, resp.GetRegisteredComponents())
		}, time.Second*10, time.Millisecond*10)

		c.encryptDecryptFail(t, ctx, client, "crypto1")
		c.encryptDecryptFail(t, ctx, client, "crypto2")
		c.encryptDecryptFail(t, ctx, client, "crypto3")
	})

	t.Run("recreating crypto component should make it available again", func(t *testing.T) {
		newComp := compapi.Component{
			TypeMeta:   metav1.TypeMeta{Kind: "Component", APIVersion: "dapr.io/v1alpha1"},
			ObjectMeta: metav1.ObjectMeta{Name: "crypto2"},
			Spec: compapi.ComponentSpec{
				Type:     "crypto.dapr.localstorage",
				Version:  "v1",
				Metadata: []common.NameValuePair{{Name: "path", Value: c.cryptoDir2JSON}},
			},
		}
		c.operator.AddComponents(newComp)
		c.operator.ComponentUpdateEvent(t, ctx, &api.ComponentUpdateEvent{Component: &newComp, EventType: operatorv1.ResourceEventType_CREATED})

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
			assert.NoError(c, err)
			assert.Len(c, resp.GetRegisteredComponents(), 2)
		}, time.Second*10, time.Millisecond*10)

		c.encryptDecryptFail(t, ctx, client, "crypto1")
		c.encryptDecrypt(t, ctx, client, "crypto2")
		c.encryptDecryptFail(t, ctx, client, "crypto3")
	})
}

func (c *crypto) encryptDecryptFail(t *testing.T, ctx context.Context, client rtv1.DaprClient, name string) {
	t.Helper()

	encclient, err := client.EncryptAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, encclient.Send(&rtv1.EncryptRequest{
		Options: &rtv1.EncryptRequestOptions{
			ComponentName:    name,
			KeyName:          name,
			KeyWrapAlgorithm: "AES",
		},
		Payload: &commonv1.StreamPayload{Data: []byte("hello"), Seq: 0},
	}))
	resp, err := encclient.Recv()
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "crypto providers not configured"),
	)
	require.Nil(t, resp)

	decclient, err := client.DecryptAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, decclient.Send(&rtv1.DecryptRequest{
		Options: &rtv1.DecryptRequestOptions{
			ComponentName: name,
			KeyName:       name,
		},
		Payload: &commonv1.StreamPayload{Seq: 0, Data: []byte("hello")},
	}))
	respd, err := decclient.Recv()
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "crypto providers not configured"),
	)
	require.Nil(t, respd)
}

func (c *crypto) encryptDecrypt(t *testing.T, ctx context.Context, client rtv1.DaprClient, name string) {
	t.Helper()

	encclient, err := client.EncryptAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, encclient.Send(&rtv1.EncryptRequest{
		Options: &rtv1.EncryptRequestOptions{
			ComponentName:    name,
			KeyName:          name,
			KeyWrapAlgorithm: "AES",
		},
		Payload: &commonv1.StreamPayload{Data: []byte("hello"), Seq: 0},
	}))
	require.NoError(t, encclient.CloseSend())
	var encdata []byte
	for {
		var resp *rtv1.EncryptResponse
		resp, err = encclient.Recv()

		if resp != nil {
			encdata = append(encdata, resp.GetPayload().GetData()...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}

	decclient, err := client.DecryptAlpha1(ctx)
	require.NoError(t, err)
	require.NoError(t, decclient.Send(&rtv1.DecryptRequest{
		Options: &rtv1.DecryptRequestOptions{
			ComponentName: name,
			KeyName:       name,
		},
		Payload: &commonv1.StreamPayload{Seq: 0, Data: encdata},
	}))
	require.NoError(t, decclient.CloseSend())
	var resp []byte
	for {
		respd, err := decclient.Recv()
		if respd != nil {
			resp = append(resp, respd.GetPayload().GetData()...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
	}
	assert.Equal(t, "hello", string(resp))
}
