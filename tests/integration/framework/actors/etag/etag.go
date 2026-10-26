/*
Copyright 2026 The Dapr Authors
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

package etag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

type Op struct {
	Key, Value, ETag   string
	CreateOnly, Delete bool
}

func GetGRPC(ctx context.Context, cl rtv1.DaprClient, actorType, actorID, key string) (string, string, error) {
	resp, err := cl.GetActorState(ctx, &rtv1.GetActorStateRequest{ActorType: actorType, ActorId: actorID, Key: key}, grpc.WaitForReady(true))
	if err != nil {
		return "", "", err
	}
	return strings.Trim(string(resp.GetData()), `"`), resp.GetEtag(), nil
}

func GetHTTP(ctx context.Context, cl *http.Client, addr, actorType, actorID, key string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stateURL(addr, actorType, actorID)+"/"+key, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := cl.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return "", "", fmt.Errorf("get state: %d %s", resp.StatusCode, b)
	}
	return strings.Trim(string(b), `"`), resp.Header.Get("ETag"), nil
}

func SaveGRPC(ctx context.Context, cl rtv1.DaprClient, actorType, actorID string, returnETags bool, ops ...Op) (map[string]string, string, error) {
	in := &rtv1.ExecuteActorStateTransactionRequest{ActorType: actorType, ActorId: actorID, ReturnEtags: returnETags}
	for _, o := range ops {
		op := &rtv1.TransactionalActorStateOperation{OperationType: "upsert", Key: o.Key, Etag: o.ETag, CreateOnly: o.CreateOnly}
		if o.Delete {
			op.OperationType = "delete"
		} else {
			op.Value = &anypb.Any{Value: []byte(o.Value)}
		}
		in.Operations = append(in.Operations, op)
	}
	resp, err := cl.ExecuteActorStateTransaction(ctx, in, grpc.WaitForReady(true))
	if err != nil {
		return nil, status.Code(err).String(), nil
	}
	if !returnETags && proto.Size(resp) != 0 {
		return nil, "", fmt.Errorf("non-empty response without opt-in: %v", resp)
	}
	return resp.GetEtags(), "ok", nil
}

func SaveHTTP(ctx context.Context, cl *http.Client, addr, actorType, actorID string, returnETags bool, ops ...Op) (map[string]string, string, error) {
	reqs := make([]map[string]any, 0, len(ops))
	for _, o := range ops {
		req := map[string]any{"key": o.Key}
		if o.ETag != "" {
			req["etag"] = o.ETag
		}
		if o.CreateOnly {
			req["createOnly"] = true
		}
		op := "delete"
		if !o.Delete {
			op = "upsert"
			req["value"] = o.Value
		}
		reqs = append(reqs, map[string]any{"operation": op, "request": req})
	}
	body, err := json.Marshal(reqs)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, stateURL(addr, actorType, actorID), bytes.NewReader(body))
	if err != nil {
		return nil, "", err
	}
	if returnETags {
		req.Header.Set("dapr-return-etags", "true")
	}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	switch {
	case !returnETags && resp.StatusCode == http.StatusNoContent:
		if len(b) > 0 {
			return nil, "", fmt.Errorf("non-empty response without opt-in: %s", b)
		}
		return nil, "ok", nil
	case returnETags && resp.StatusCode == http.StatusOK:
		if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
			return nil, "", fmt.Errorf("unexpected content type %q", ct)
		}
		var res struct {
			ETags map[string]string `json:"etags"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			return nil, "", fmt.Errorf("%w: %s", err, b)
		}
		if res.ETags == nil {
			return nil, "", fmt.Errorf("no etags in %s", b)
		}
		return res.ETags, "ok", nil
	}

	var apiErr struct {
		ErrorCode string `json:"errorCode"`
	}
	if err := json.Unmarshal(b, &apiErr); err != nil {
		return nil, "", fmt.Errorf("%d: %w: %s", resp.StatusCode, err, b)
	}
	return nil, fmt.Sprintf("%d %s", resp.StatusCode, apiErr.ErrorCode), nil
}

func stateURL(addr, actorType, actorID string) string {
	return "http://" + addr + "/v1.0/actors/" + actorType + "/" + actorID + "/state"
}
