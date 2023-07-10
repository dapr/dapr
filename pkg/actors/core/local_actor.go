package core

import (
	"context"
	"errors"
	"fmt"
	nethttp "net/http"
	"strings"
	"sync"

	"github.com/gogo/status"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/actors/core/reminder"
	"github.com/dapr/dapr/pkg/channel"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/resiliency"
)

var ErrDaprResponseHeader = errors.New("error indicated via actor header response")

type LocalActor struct {
	config               Config
	resiliency           resiliency.Provider
	internalActorChannel *InternalActorChannel
	appChannel           channel.AppChannel
	actorsTable          *sync.Map
	clock                clock.WithTicker
}

type LocalActorOpts struct {
	Config               Config
	Resiliency           resiliency.Provider
	AppChannel           channel.AppChannel
	ActorsTable          *sync.Map
	InternalActorChannel *InternalActorChannel
	Clock                clock.WithTicker
}

func NewLocalActor(localActorOpts LocalActorOpts, clock clock.WithTicker) *LocalActor {
	return &LocalActor{
		config:               localActorOpts.Config,
		resiliency:           localActorOpts.Resiliency,
		internalActorChannel: localActorOpts.InternalActorChannel,
		appChannel:           localActorOpts.AppChannel,
		actorsTable:          localActorOpts.ActorsTable,
		clock:                localActorOpts.Clock,
	}
}

func (l *LocalActor) CallLocalActor(ctx context.Context, req *invokev1.InvokeMethodRequest) (*invokev1.InvokeMethodResponse, error) {
	actorTypeID := req.Actor()

	act := l.GetOrCreateActor(actorTypeID.GetActorType(), actorTypeID.GetActorId())

	// Reentrancy to determine how we lock.
	var reentrancyID *string
	if l.config.GetReentrancyForType(act.ActorType).Enabled {
		if headerValue, ok := req.Metadata()["Dapr-Reentrancy-Id"]; ok {
			reentrancyID = &headerValue.GetValues()[0]
		} else {
			// reentrancyHeader := fasthttp.RequestHeader{}
			uuidObj, err := uuid.NewRandom()
			if err != nil {
				return nil, fmt.Errorf("failed to generate UUID: %w", err)
			}
			uuid := uuidObj.String()
			req.AddMetadata(map[string][]string{
				"Dapr-Reentrancy-Id": {uuid},
			})
			reentrancyID = &uuid
		}
	}

	err := act.Lock(reentrancyID)
	if err != nil {
		return nil, status.Error(codes.ResourceExhausted, err.Error())
	}
	defer act.Unlock()

	// Replace method to actors method.
	msg := req.Message()
	originalMethod := msg.Method
	msg.Method = "actors/" + actorTypeID.ActorType + "/" + actorTypeID.ActorId + "/method/" + msg.Method

	// Reset the method so we can perform retries.
	defer func() {
		msg.Method = originalMethod
	}()

	// Original code overrides method with PUT. Why?
	if msg.GetHttpExtension() == nil {
		req.WithHTTPExtension(nethttp.MethodPut, "")
	} else {
		msg.HttpExtension.Verb = commonv1pb.HTTPExtension_PUT //nolint:nosnakecase
	}

	policyDef := l.resiliency.ActorPostLockPolicy(act.ActorType, act.ActorID)

	// If the request can be retried, we need to enable replaying
	if policyDef != nil && policyDef.HasRetries() {
		req.WithReplay(true)
	}

	policyRunner := resiliency.NewRunnerWithOptions(ctx, policyDef,
		resiliency.RunnerOpts[*invokev1.InvokeMethodResponse]{
			Disposer: resiliency.DisposerCloser[*invokev1.InvokeMethodResponse],
		},
	)
	resp, err := policyRunner(func(ctx context.Context) (*invokev1.InvokeMethodResponse, error) {
		return l.GetAppChannel(act.ActorType).InvokeMethod(ctx, req, "")
	})
	if err != nil {
		return nil, err
	}

	if resp == nil {
		return nil, errors.New("error from actor service: response object is nil")
	}

	if resp.Status().Code != nethttp.StatusOK {
		respData, _ := resp.RawDataFull()
		return nil, fmt.Errorf("error from actor service: %s", string(respData))
	}

	// The .NET SDK signifies Actor failure via a header instead of a bad response.
	if _, ok := resp.Headers()["X-Daprerrorresponseheader"]; ok {
		return resp, ErrDaprResponseHeader
	}

	return resp, nil
}

func (l *LocalActor) GetAppChannel(actorType string) channel.AppChannel {
	if l.internalActorChannel.Contains(actorType) {
		return l.internalActorChannel
	}
	return l.appChannel
}

func (l *LocalActor) GetOrCreateActor(actorType, actorID string) *Actor {
	key := constructCompositeKey(actorType, actorID)

	// This avoids allocating multiple actor allocations by calling newActor
	// whenever actor is invoked. When storing actor key first, there is a chance to
	// call newActor, but this is trivial.
	val, ok := l.actorsTable.Load(key)
	if !ok {
		val, _ = l.actorsTable.LoadOrStore(key, newActor(actorType, actorID, l.config.GetReentrancyForType(actorType).MaxStackDepth, l.clock))
	}

	return val.(*Actor)
}

func constructCompositeKey(keys ...string) string {
	return strings.Join(keys, reminder.DaprSeparator)
}

func newActor(actorType, actorID string, maxReentrancyDepth *int, cl clock.Clock) *Actor {
	if cl == nil {
		cl = &clock.RealClock{}
	}
	return &Actor{
		ActorType:    actorType,
		ActorID:      actorID,
		ActorLock:    NewActorLock(int32(*maxReentrancyDepth)),
		Clock:        cl,
		LastUsedTime: cl.Now().UTC(),
	}
}

func NewActorLock(maxStackDepth int32) *ActorLock {
	return &ActorLock{
		maxStackDepth: maxStackDepth,
	}
}
