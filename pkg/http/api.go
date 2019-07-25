package http

import (
	"fmt"
	"strconv"
	"strings"

	jsoniter "github.com/json-iterator/go"

	"github.com/actionscore/actions/pkg/actors"
	"github.com/actionscore/actions/pkg/channel"
	"github.com/actionscore/actions/pkg/channel/http"
	"github.com/actionscore/actions/pkg/components/state"
	"github.com/actionscore/actions/pkg/messaging"
	routing "github.com/qiangxue/fasthttp-routing"
)

type API interface {
	APIEndpoints() []Endpoint
}

type api struct {
	endpoints       []Endpoint
	directMessaging messaging.DirectMessaging
	appChannel      channel.AppChannel
	stateStore      state.StateStore
	json            jsoniter.API
	actor           actors.Actors
	id              string
}

const (
	apiVersionV1   = "v1.0"
	idParam        = "id"
	methodParam    = "method"
	actorTypeParam = "actorType"
	actorIDParam   = "actorId"
	stateKeyParam  = "key"
)

func NewAPI(actionID string, appChannel channel.AppChannel, directMessaging messaging.DirectMessaging, stateStore state.StateStore, actor actors.Actors) API {
	api := &api{
		appChannel:      appChannel,
		directMessaging: directMessaging,
		stateStore:      stateStore,
		json:            jsoniter.ConfigFastest,
		actor:           actor,
		id:              actionID,
	}
	api.endpoints = append(api.endpoints, api.constructStateEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructPubSubEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructActorEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructDirectMessagingEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructMetadataEndpoints()...)

	return api
}

func (a *api) APIEndpoints() []Endpoint {
	return a.endpoints
}

func (a *api) constructStateEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Get},
			Route:   "state/<key>",
			Version: apiVersionV1,
			Handler: a.onGetState,
		},
		{
			Methods: []string{http.Post},
			Route:   "state",
			Version: apiVersionV1,
			Handler: a.onPostState,
		},
		{
			Methods: []string{http.Delete},
			Route:   "state/<key>",
			Version: apiVersionV1,
			Handler: a.onDeleteState,
		},
	}
}

func (a *api) constructPubSubEndpoints() []Endpoint {
	return []Endpoint{}
}

func (a *api) constructDirectMessagingEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Get, http.Post, http.Delete, http.Put},
			Route:   "actions/<id>/<method>",
			Version: apiVersionV1,
			Handler: a.onDirectMessage,
		},
		{
			Methods: []string{http.Post},
			Route:   "invoke/*",
			Version: apiVersionV1,
			Handler: a.onInvokeLocal,
		},
	}
}

func (a *api) constructActorEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Get, http.Post, http.Delete, http.Put},
			Route:   "actors/<actorType>/<actorId>/<method>",
			Version: apiVersionV1,
			Handler: a.onDirectActorMessage,
		},
		{
			Methods: []string{http.Post, http.Put},
			Route:   "actors/<actorType>/<actorId>/state",
			Version: apiVersionV1,
			Handler: a.OnSaveActorState,
		},
		{
			Methods: []string{http.Get},
			Route:   "actors/<actorType>/<actorId>/state",
			Version: apiVersionV1,
			Handler: a.onGetActorState,
		},
	}
}

func (a *api) constructMetadataEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Get},
			Route:   "metadata",
			Version: apiVersionV1,
			Handler: a.onGetMetadata,
		},
	}
}

func (a *api) onGetState(c *routing.Context) error {
	if a.stateStore == nil {
		respondWithError(c.RequestCtx, 400, "error: state store not found")
		return nil
	}

	key := c.Param(stateKeyParam)
	req := state.GetRequest{
		Key: a.getModifiedStateKey(key),
	}

	resp, err := a.stateStore.Get(&req)
	if err != nil {
		respondWithError(c.RequestCtx, 500, fmt.Sprintf("error getting state: %s", err))
		return nil
	}

	respondWithJSON(c.RequestCtx, 200, resp.Data)
	return nil
}

func (a *api) onDeleteState(c *routing.Context) error {
	if a.stateStore == nil {
		respondWithError(c.RequestCtx, 400, "error: state store not found")
		return nil
	}

	key := c.Param(stateKeyParam)
	req := state.DeleteRequest{
		Key: key,
	}

	err := a.stateStore.Delete(&req)
	if err != nil {
		respondWithError(c.RequestCtx, 500, fmt.Sprintf("error deleting state with key %s: %s", key, err))
		return nil
	}

	return nil
}

func (a *api) onPostState(c *routing.Context) error {
	if a.stateStore == nil {
		respondWithError(c.RequestCtx, 400, "error: state store not found")
		return nil
	}

	reqs := []state.SetRequest{}
	err := a.json.Unmarshal(c.PostBody(), &reqs)
	if err != nil {
		respondWithError(c.RequestCtx, 400, "error: malformed json request")
		return nil
	}

	for i, r := range reqs {
		reqs[i].Key = a.getModifiedStateKey(r.Key)
	}

	err = a.stateStore.BulkSet(reqs)
	if err != nil {
		respondWithError(c.RequestCtx, 500, fmt.Sprintf("error saving state: %s", err))
		return nil
	}

	respondEmpty(c.RequestCtx, 201)

	return nil
}

func (a *api) getModifiedStateKey(key string) string {
	if a.id != "" {
		return fmt.Sprintf("%s-%s", a.id, key)
	}

	return key
}

func (a *api) onDirectMessage(c *routing.Context) error {
	targetID := c.Param(idParam)
	method := c.Param(methodParam)
	body := c.PostBody()
	verb := string(c.Method())

	req := messaging.DirectMessageRequest{
		Data:     body,
		Method:   method,
		Metadata: map[string]string{http.HTTPVerb: verb},
		Target:   targetID,
	}

	resp, err := a.directMessaging.Invoke(&req)
	if err != nil {
		respondWithError(c.RequestCtx, 500, err.Error())
	} else {
		statusCode := GetStatusCodeFromMetadata(resp.Metadata)
		respondWithJSON(c.RequestCtx, statusCode, resp.Data)
	}

	return nil
}

func (a *api) onInvokeLocal(c *routing.Context) error {
	method := string(c.Path())[len(string(c.Path()))-strings.Index(string(c.Path()), "invoke/"):]
	body := c.PostBody()
	verb := string(c.Method())

	req := channel.InvokeRequest{
		Metadata: map[string]string{http.HTTPVerb: verb},
		Payload:  body,
		Method:   method,
	}
	resp, err := a.appChannel.InvokeMethod(&req)
	if err != nil {
		respondWithError(c.RequestCtx, 500, err.Error())
	} else {
		statusCode := GetStatusCodeFromMetadata(resp.Metadata)
		respondWithJSON(c.RequestCtx, statusCode, resp.Data)
	}

	return nil
}

func (a *api) onDirectActorMessage(c *routing.Context) error {
	if a.actor == nil {
		respondWithError(c.RequestCtx, 400, "actors not initialized")
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	method := c.Param(methodParam)
	body := c.PostBody()

	req := actors.CallRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Method:    method,
		Metadata:  map[string]string{},
		Data:      body,
	}

	resp, err := a.actor.Call(&req)
	if err != nil {
		respondWithError(c.RequestCtx, 500, err.Error())
	} else {
		respondWithJSON(c.RequestCtx, 200, resp.Data)
	}

	return nil
}

func (a *api) OnSaveActorState(c *routing.Context) error {
	if a.actor == nil {
		respondWithError(c.RequestCtx, 400, "actors not initialized")
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	body := c.PostBody()

	var state actors.SaveStateRequest
	err := a.json.Unmarshal(body, &state)
	if err != nil {
		respondWithError(c.RequestCtx, 400, "error: malformed json request")
		return nil
	}

	req := actors.SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Data:      body,
	}

	err = a.actor.SaveState(&req)
	if err != nil {
		respondWithError(c.RequestCtx, 500, err.Error())
	} else {
		respondEmpty(c.RequestCtx, 201)
	}

	return nil
}

func (a *api) onGetActorState(c *routing.Context) error {
	if a.actor == nil {
		respondWithError(c.RequestCtx, 400, "actors not initialized")
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)

	req := actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
	}

	resp, err := a.actor.GetState(&req)
	if err != nil {
		respondWithError(c.RequestCtx, 500, err.Error())
	} else {
		respondWithJSON(c.RequestCtx, 200, resp.Data)
	}

	return nil
}

func (a *api) onGetMetadata(c *routing.Context) error {
	//TODO: implement
	return nil
}

// GetStatusCodeFromMetadata extracts the http status code from the metadata if it exists
func GetStatusCodeFromMetadata(metadata map[string]string) int {
	code := metadata[http.HTTPStatusCode]
	if code != "" {
		statusCode, err := strconv.Atoi(code)
		if err == nil {
			return statusCode
		}
	}

	return 200
}
