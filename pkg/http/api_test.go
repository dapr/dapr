package http

import (
	"testing"

	"github.com/actionscore/actions/pkg/channel"
	"github.com/actionscore/actions/pkg/channel/http"
	"github.com/actionscore/actions/pkg/messaging"
	routing "github.com/qiangxue/fasthttp-routing"
	"github.com/stretchr/testify/assert"
	"github.com/valyala/fasthttp"
)

type mockChannel struct {
}

func (c mockChannel) InvokeMethod(req *channel.InvokeRequest) (*channel.InvokeResponse, error) {
	if val, ok := req.Metadata[http.QueryString]; ok {
		return &channel.InvokeResponse{Data: []byte(val)}, nil
	}
	return nil, nil
}

type mockDirectMessaging struct {
	appChannel channel.AppChannel
}

func (d mockDirectMessaging) Invoke(req *messaging.DirectMessageRequest) (*messaging.DirectMessageResponse, error) {
	localInvokeReq := channel.InvokeRequest{
		Metadata: req.Metadata,
		Method:   req.Method,
		Payload:  req.Data,
	}

	resp, err := d.appChannel.InvokeMethod(&localInvokeReq)
	if err != nil {
		return nil, err
	}

	return &messaging.DirectMessageResponse{
		Data:     resp.Data,
		Metadata: resp.Metadata,
	}, nil
}

func TestOnDirectMessage(t *testing.T) {
	testAPI := &api{directMessaging: mockDirectMessaging{appChannel: mockChannel{}}}
	c := &routing.Context{}
	request := fasthttp.Request{}
	request.URI().Parse(nil, []byte("http://www.microsoft.com/dummy?param1=val1&param2=val2"))
	c.RequestCtx = &fasthttp.RequestCtx{Request: request}
	err := testAPI.onDirectMessage(c)
	assert.NoError(t, err)
	assert.Equal(t, "param1=val1&param2=val2", string(c.Response.Body()))
}
