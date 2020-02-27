// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/channel"
	"github.com/dapr/dapr/pkg/channel/http"
	"github.com/dapr/dapr/pkg/messaging"
	"github.com/google/uuid"
	jsoniter "github.com/json-iterator/go"
	routing "github.com/qiangxue/fasthttp-routing"
)

// API returns a list of HTTP endpoints for Dapr
type API interface {
	APIEndpoints() []Endpoint
}

type api struct {
	endpoints             []Endpoint
	directMessaging       messaging.DirectMessaging
	appChannel            channel.AppChannel
	stateStores           map[string]state.Store
	secretStores          map[string]secretstores.SecretStore
	json                  jsoniter.API
	actor                 actors.Actors
	pubSub                pubsub.PubSub
	sendToOutputBindingFn func(name string, req *bindings.WriteRequest) error
	id                    string
}

type metadata struct {
	ID                string                     `json:"id"`
	ActiveActorsCount []actors.ActiveActorsCount `json:"actors"`
}

const (
	apiVersionV1         = "v1.0"
	idParam              = "id"
	methodParam          = "method"
	actorTypeParam       = "actorType"
	actorIDParam         = "actorId"
	storeNameParam       = "storeName"
	stateKeyParam        = "key"
	secretStoreNameParam = "secretStoreName"
	secretNameParam      = "key"
	topicParam           = "topic"
	nameParam            = "name"
	consistencyParam     = "consistency"
	retryIntervalParam   = "retryInterval"
	retryPatternParam    = "retryPattern"
	retryThresholdParam  = "retryThreshold"
	concurrencyParam     = "concurrency"
	daprSeparator        = "||"
)

// NewAPI returns a new API
func NewAPI(appID string, appChannel channel.AppChannel, directMessaging messaging.DirectMessaging, stateStores map[string]state.Store, secretStores map[string]secretstores.SecretStore, pubSub pubsub.PubSub, actor actors.Actors, sendToOutputBindingFn func(name string, req *bindings.WriteRequest) error) API {
	api := &api{
		appChannel:            appChannel,
		directMessaging:       directMessaging,
		stateStores:           stateStores,
		secretStores:          secretStores,
		json:                  jsoniter.ConfigFastest,
		actor:                 actor,
		pubSub:                pubSub,
		sendToOutputBindingFn: sendToOutputBindingFn,
		id:                    appID,
	}
	api.endpoints = append(api.endpoints, api.constructStateEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructSecretEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructPubSubEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructActorEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructDirectMessagingEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructMetadataEndpoints()...)
	api.endpoints = append(api.endpoints, api.constructBindingsEndpoints()...)

	return api
}

// APIEndpoints returns the list of registered endpoints
func (a *api) APIEndpoints() []Endpoint {
	return a.endpoints
}

func (a *api) constructStateEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Get},
			Route:   "state/<storeName>/<key>",
			Version: apiVersionV1,
			Handler: a.onGetState,
		},
		{
			Methods: []string{http.Post},
			Route:   "state/<storeName>",
			Version: apiVersionV1,
			Handler: a.onPostState,
		},
		{
			Methods: []string{http.Delete},
			Route:   "state/<storeName>/<key>",
			Version: apiVersionV1,
			Handler: a.onDeleteState,
		},
	}
}

func (a *api) constructSecretEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Get},
			Route:   "secrets/<secretStoreName>/<key>",
			Version: apiVersionV1,
			Handler: a.onGetSecret,
		},
	}
}

func (a *api) constructPubSubEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Post, http.Put},
			Route:   "publish/<topic>",
			Version: apiVersionV1,
			Handler: a.onPublish,
		},
	}
}

func (a *api) constructBindingsEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Post, http.Put},
			Route:   "bindings/<name>",
			Version: apiVersionV1,
			Handler: a.onOutputBindingMessage,
		},
	}
}

func (a *api) constructDirectMessagingEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Get, http.Post, http.Delete, http.Put},
			Route:   "invoke/<id>/method/*",
			Version: apiVersionV1,
			Handler: a.onDirectMessage,
		},
	}
}

func (a *api) constructActorEndpoints() []Endpoint {
	return []Endpoint{
		{
			Methods: []string{http.Post, http.Put},
			Route:   "actors/<actorType>/<actorId>/state",
			Version: apiVersionV1,
			Handler: a.onActorStateTransaction,
		},
		{
			Methods: []string{http.Get, http.Post, http.Delete, http.Put},
			Route:   "actors/<actorType>/<actorId>/method/<method>",
			Version: apiVersionV1,
			Handler: a.onDirectActorMessage,
		},
		{
			Methods: []string{http.Post, http.Put},
			Route:   "actors/<actorType>/<actorId>/state/<key>",
			Version: apiVersionV1,
			Handler: a.onSaveActorState,
		},
		{
			Methods: []string{http.Get},
			Route:   "actors/<actorType>/<actorId>/state/<key>",
			Version: apiVersionV1,
			Handler: a.onGetActorState,
		},
		{
			Methods: []string{http.Delete},
			Route:   "actors/<actorType>/<actorId>/state/<key>",
			Version: apiVersionV1,
			Handler: a.onDeleteActorState,
		},
		{
			Methods: []string{http.Post, http.Put},
			Route:   "actors/<actorType>/<actorId>/reminders/<name>",
			Version: apiVersionV1,
			Handler: a.onCreateActorReminder,
		},
		{
			Methods: []string{http.Post, http.Put},
			Route:   "actors/<actorType>/<actorId>/timers/<name>",
			Version: apiVersionV1,
			Handler: a.onCreateActorTimer,
		},
		{
			Methods: []string{http.Delete},
			Route:   "actors/<actorType>/<actorId>/reminders/<name>",
			Version: apiVersionV1,
			Handler: a.onDeleteActorReminder,
		},
		{
			Methods: []string{http.Delete},
			Route:   "actors/<actorType>/<actorId>/timers/<name>",
			Version: apiVersionV1,
			Handler: a.onDeleteActorTimer,
		},
		{
			Methods: []string{http.Get},
			Route:   "actors/<actorType>/<actorId>/reminders/<name>",
			Version: apiVersionV1,
			Handler: a.onGetActorReminder,
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

func (a *api) onOutputBindingMessage(c *routing.Context) error {
	name := c.Param(nameParam)
	body := c.PostBody()

	var req OutputBindingRequest
	err := a.json.Unmarshal(body, &req)
	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf("can't deserialize request: %s", err))
		respondWithError(c.RequestCtx, 500, msg)
		return nil
	}

	b, err := a.json.Marshal(req.Data)
	if err != nil {
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", fmt.Sprintf("can't deserialize request data field: %s", err))
		respondWithError(c.RequestCtx, 500, msg)
		return nil
	}
	err = a.sendToOutputBindingFn(name, &bindings.WriteRequest{
		Metadata: req.Metadata,
		Data:     b,
	})
	if err != nil {
		errMsg := fmt.Sprintf("error invoking output binding %s: %s", name, err)
		msg := NewErrorResponse("ERR_INVOKE_OUTPUT_BINDING", errMsg)
		respondWithError(c.RequestCtx, 500, msg)
		return nil
	}

	respondEmpty(c.RequestCtx, 200)
	return nil
}

func (a *api) onGetState(c *routing.Context) error {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_CONFIGURED", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	storeName := c.Param(storeNameParam)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf("state store name: %s", storeName))
		respondWithError(c.RequestCtx, 401, msg)
		return nil
	}

	key := c.Param(stateKeyParam)
	consistency := string(c.QueryArgs().Peek(consistencyParam))
	req := state.GetRequest{
		Key: a.getModifiedStateKey(key),
		Options: state.GetStateOption{
			Consistency: consistency,
		},
	}

	resp, err := a.stateStores[storeName].Get(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_GET", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
		return nil
	}
	if resp == nil || resp.Data == nil {
		respondEmpty(c.RequestCtx, 204)
		return nil
	}
	respondWithETaggedJSON(c.RequestCtx, 200, resp.Data, resp.ETag)
	return nil
}

func (a *api) onDeleteState(c *routing.Context) error {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORES_NOT_CONFIGURED", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	storeName := c.Param(storeNameParam)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf("state store name: %s", storeName))
		respondWithError(c.RequestCtx, 401, msg)
		return nil
	}

	key := c.Param(stateKeyParam)
	etag := string(c.Request.Header.Peek("If-Match"))

	concurrency := string(c.QueryArgs().Peek(concurrencyParam))
	consistency := string(c.QueryArgs().Peek(consistencyParam))
	retryInterval := string(c.QueryArgs().Peek(retryIntervalParam))
	retryPattern := string(c.QueryArgs().Peek(retryPatternParam))
	retryThredhold := string(c.QueryArgs().Peek(retryThresholdParam))
	iRetryInterval := 0
	iRetryThreshold := 0

	if retryInterval != "" {
		iRetryInterval, _ = strconv.Atoi(retryInterval)
	}
	if retryThredhold != "" {
		iRetryThreshold, _ = strconv.Atoi(retryThredhold)
	}

	req := state.DeleteRequest{
		Key:  a.getModifiedStateKey(key),
		ETag: etag,
		Options: state.DeleteStateOption{
			Concurrency: concurrency,
			Consistency: consistency,
			RetryPolicy: state.RetryPolicy{
				Interval:  time.Duration(iRetryInterval) * time.Millisecond,
				Threshold: iRetryThreshold,
				Pattern:   retryPattern,
			},
		},
	}

	err := a.stateStores[storeName].Delete(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_DELETE", fmt.Sprintf("failed deleting state with key %s: %s", key, err))
		respondWithError(c.RequestCtx, 500, msg)
		return nil
	}

	return nil
}

func (a *api) onGetSecret(c *routing.Context) error {
	if a.secretStores == nil || len(a.secretStores) == 0 {
		msg := NewErrorResponse("ERR_SECRET_STORE_NOT_CONFIGURED", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	secretStoreName := c.Param(secretStoreNameParam)

	if a.secretStores[secretStoreName] == nil {
		msg := NewErrorResponse("ERR_SECRET_STORE_NOT_FOUND", fmt.Sprintf("secret store name: %s", secretStoreName))
		respondWithError(c.RequestCtx, 401, msg)
		return nil
	}

	metadata := map[string]string{}
	const metadataPrefix string = "metadata."
	c.QueryArgs().VisitAll(func(key []byte, value []byte) {
		queryKey := string(key)
		if strings.HasPrefix(queryKey, metadataPrefix) {
			k := strings.TrimPrefix(queryKey, metadataPrefix)
			metadata[k] = string(value)
		}
	})

	key := c.Param(secretNameParam)
	req := secretstores.GetSecretRequest{
		Name:     key,
		Metadata: metadata,
	}

	resp, err := a.secretStores[secretStoreName].GetSecret(req)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_GET", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
		return nil
	}

	if resp.Data == nil {
		respondEmpty(c.RequestCtx, 204)
		return nil
	}

	respBytes, _ := a.json.Marshal(resp.Data)
	respondWithJSON(c.RequestCtx, 200, respBytes)
	return nil
}

func (a *api) onPostState(c *routing.Context) error {
	if a.stateStores == nil || len(a.stateStores) == 0 {
		msg := NewErrorResponse("ERR_STATE_STORES_NOT_CONFIGURED", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	storeName := c.Param(storeNameParam)

	if a.stateStores[storeName] == nil {
		msg := NewErrorResponse("ERR_STATE_STORE_NOT_FOUND", fmt.Sprintf("state store name: %s", storeName))
		respondWithError(c.RequestCtx, 401, msg)
		return nil
	}

	reqs := []state.SetRequest{}
	err := a.json.Unmarshal(c.PostBody(), &reqs)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(c.RequestCtx, 402, msg)
		return nil
	}

	for i, r := range reqs {
		reqs[i].Key = a.getModifiedStateKey(r.Key)
	}

	err = a.stateStores[storeName].BulkSet(reqs)
	if err != nil {
		msg := NewErrorResponse("ERR_STATE_SAVE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
		return nil
	}

	respondEmpty(c.RequestCtx, 201)

	return nil
}

func (a *api) getModifiedStateKey(key string) string {
	if a.id != "" {
		return fmt.Sprintf("%s%s%s", a.id, daprSeparator, key)
	}

	return key
}

func (a *api) setHeaders(c *routing.Context, metadata map[string]string) {
	headers := []string{}
	c.RequestCtx.Request.Header.VisitAll(func(key, value []byte) {
		k := string(key)
		v := string(value)

		headers = append(headers, fmt.Sprintf("%s&__header_equals__&%s", k, v))
	})
	if len(headers) > 0 {
		metadata["headers"] = strings.Join(headers, "&__header_delim__&")
	}
}

func (a *api) onDirectMessage(c *routing.Context) error {
	targetID := c.Param(idParam)
	path := string(c.Path())
	method := path[strings.Index(path, "method/")+7:]
	body := c.PostBody()
	verb := string(c.Method())
	queryString := string(c.QueryArgs().QueryString())

	req := messaging.DirectMessageRequest{
		Data:     body,
		Method:   method,
		Metadata: map[string]string{http.HTTPVerb: verb, http.QueryString: queryString},
		Target:   targetID,
	}
	a.setHeaders(c, req.Metadata)

	resp, err := a.directMessaging.Invoke(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_DIRECT_INVOKE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		statusCode := GetStatusCodeFromMetadata(resp.Metadata)
		a.setHeadersOnRequest(resp.Metadata, c)
		respond(c.RequestCtx, statusCode, resp.Data)
	}

	return nil
}

func (a *api) onCreateActorReminder(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	name := c.Param(nameParam)

	var req actors.CreateReminderRequest
	err := a.json.Unmarshal(c.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateReminder(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_CREATE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondEmpty(c.RequestCtx, 200)
	}

	return nil
}

func (a *api) onCreateActorTimer(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	name := c.Param(nameParam)

	var req actors.CreateTimerRequest
	err := a.json.Unmarshal(c.PostBody(), &req)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	req.Name = name
	req.ActorType = actorType
	req.ActorID = actorID

	err = a.actor.CreateTimer(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_CREATE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondEmpty(c.RequestCtx, 200)
	}

	return nil
}

func (a *api) onDeleteActorReminder(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	name := c.Param(nameParam)

	req := actors.DeleteReminderRequest{
		Name:      name,
		ActorID:   actorID,
		ActorType: actorType,
	}

	err := a.actor.DeleteReminder(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_DELETE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondEmpty(c.RequestCtx, 200)
	}

	return nil
}

func (a *api) onActorStateTransaction(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	body := c.PostBody()

	hosted := a.actor.IsActorHosted(&actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	var ops []actors.TransactionalOperation
	err := a.json.Unmarshal(body, &ops)
	if err != nil {
		msg := NewErrorResponse("ERR_MALFORMED_REQUEST", err.Error())
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	req := actors.TransactionalRequest{
		ActorID:    actorID,
		ActorType:  actorType,
		Operations: ops,
	}

	err = a.actor.TransactionalStateOperation(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_TRANSACTION_SAVE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondEmpty(c.RequestCtx, 201)
	}

	return nil
}

func (a *api) onGetActorReminder(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	name := c.Param(nameParam)

	resp, err := a.actor.GetReminder(&actors.GetReminderRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Name:      name,
	})
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	}
	b, err := a.json.Marshal(resp)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_REMINDER_GET", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondWithJSON(c.RequestCtx, 200, b)
	}
	return nil
}

func (a *api) onDeleteActorTimer(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	name := c.Param(nameParam)

	req := actors.DeleteTimerRequest{
		Name:      name,
		ActorID:   actorID,
		ActorType: actorType,
	}

	err := a.actor.DeleteTimer(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_TIMER_DELETE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondEmpty(c.RequestCtx, 200)
	}

	return nil
}

func (a *api) onDirectActorMessage(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
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
	a.setHeaders(c, req.Metadata)

	resp, err := a.actor.Call(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_INVOKE_METHOD", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		statusCode := GetStatusCodeFromMetadata(resp.Metadata)
		a.setHeadersOnRequest(resp.Metadata, c)
		respondWithJSON(c.RequestCtx, statusCode, resp.Data)
	}

	return nil
}

func (a *api) setHeadersOnRequest(metadata map[string]string, c *routing.Context) {
	if metadata == nil {
		return
	}

	if val, ok := metadata["headers"]; ok {
		headers := strings.Split(val, "&__header_delim__&")
		for _, h := range headers {
			kv := strings.Split(h, "&__header_equals__&")
			c.RequestCtx.Response.Header.Set(kv[0], kv[1])
		}
	}
}

func (a *api) onSaveActorState(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	key := c.Param(stateKeyParam)
	body := c.PostBody()

	hosted := a.actor.IsActorHosted(&actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	// Deserialize body to validate JSON compatible body
	// and remove useless characters before saving
	var val interface{}
	err := a.json.Unmarshal(body, &val)
	if err != nil {
		msg := NewErrorResponse("ERR_DESERIALIZE_HTTP_BODY", err.Error())
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	req := actors.SaveStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       key,
		Value:     val,
	}

	err = a.actor.SaveState(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_SAVE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondEmpty(c.RequestCtx, 201)
	}

	return nil
}

func (a *api) onGetActorState(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	key := c.Param(stateKeyParam)

	req := actors.GetStateRequest{
		ActorType: actorType,
		ActorID:   actorID,
		Key:       key,
	}

	resp, err := a.actor.GetState(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_GET", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondWithJSON(c.RequestCtx, 200, resp.Data)
	}

	return nil
}

func (a *api) onDeleteActorState(c *routing.Context) error {
	if a.actor == nil {
		msg := NewErrorResponse("ERR_ACTOR_RUNTIME_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	actorType := c.Param(actorTypeParam)
	actorID := c.Param(actorIDParam)
	key := c.Param(stateKeyParam)

	hosted := a.actor.IsActorHosted(&actors.ActorHostedRequest{
		ActorType: actorType,
		ActorID:   actorID,
	})

	if !hosted {
		msg := NewErrorResponse("ERR_ACTOR_INSTANCE_MISSING", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	req := actors.DeleteStateRequest{
		ActorID:   actorID,
		ActorType: actorType,
		Key:       key,
	}

	err := a.actor.DeleteState(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_ACTOR_STATE_DELETE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondEmpty(c.RequestCtx, 200)
	}

	return nil
}

func (a *api) onGetMetadata(c *routing.Context) error {
	mtd := metadata{
		ID:                a.id,
		ActiveActorsCount: a.actor.GetActiveActorsCount(),
	}

	mtdBytes, err := a.json.Marshal(mtd)
	if err != nil {
		msg := NewErrorResponse("ERR_METADATA_GET", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondWithJSON(c.RequestCtx, 200, mtdBytes)
	}

	return nil
}

func (a *api) onPublish(c *routing.Context) error {
	if a.pubSub == nil {
		msg := NewErrorResponse("ERR_PUBSUB_NOT_FOUND", "")
		respondWithError(c.RequestCtx, 400, msg)
		return nil
	}

	topic := c.Param(topicParam)
	body := c.PostBody()

	envelope := pubsub.NewCloudEventsEnvelope(uuid.New().String(), a.id, pubsub.DefaultCloudEventType, body)
	b, err := a.json.Marshal(envelope)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_CLOUD_EVENTS_SER", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
		return nil
	}

	req := pubsub.PublishRequest{
		Topic: topic,
		Data:  b,
	}
	err = a.pubSub.Publish(&req)
	if err != nil {
		msg := NewErrorResponse("ERR_PUBSUB_PUBLISH_MESSAGE", err.Error())
		respondWithError(c.RequestCtx, 500, msg)
	} else {
		respondEmpty(c.RequestCtx, 200)
	}

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
