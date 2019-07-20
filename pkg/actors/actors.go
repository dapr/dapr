package actors

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/actionscore/actions/pkg/channel/http"

	"github.com/golang/protobuf/ptypes/any"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/channel"
	"github.com/actionscore/actions/pkg/components/state"
	"github.com/actionscore/actions/pkg/placement"
	pb "github.com/actionscore/actions/pkg/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type Actors interface {
	Call(req *CallRequest) (*CallResponse, error)
	Init() error
	GetState(req *GetStateRequest) (*StateResponse, error)
	SaveState(req *SaveStateRequest) error
}

type actors struct {
	appChannel          channel.AppChannel
	store               state.StateStore
	activationCheckLock *sync.RWMutex
	activeActorsLocks   *sync.Map
	actorLock           *sync.RWMutex
	placementTableLock  *sync.RWMutex
	placementTables     *placement.PlacementTables
	placementSignal     chan struct{}
	placementBlock      bool
	operationUpdateLock *sync.Mutex
	grpcConnectionFn    func(address string) (*grpc.ClientConn, error)
	config              Config
	actorLastUsedLock   *sync.RWMutex
	actorLastUsed       map[string]time.Time
}

const (
	idHeader        = "id"
	lockOperation   = "lock"
	unlockOperation = "unlock"
	updateOperation = "update"
)

func NewActors(stateStore state.StateStore, appChannel channel.AppChannel, grpcConnectionFn func(address string) (*grpc.ClientConn, error), config Config) Actors {
	return &actors{
		appChannel:          appChannel,
		config:              config,
		store:               stateStore,
		activationCheckLock: &sync.RWMutex{},
		actorLock:           &sync.RWMutex{},
		activeActorsLocks:   &sync.Map{},
		placementTableLock:  &sync.RWMutex{},
		placementTables:     &placement.PlacementTables{Entries: make(map[string]*placement.Consistent)},
		operationUpdateLock: &sync.Mutex{},
		grpcConnectionFn:    grpcConnectionFn,
		actorLastUsedLock:   &sync.RWMutex{},
		actorLastUsed:       map[string]time.Time{},
	}
}

func (a *actors) Init() error {
	if a.config.PlacementServiceAddress == "" {
		return errors.New("couldn't connect to placement service: address is empty")
	}

	go a.connectToPlacementService(a.config.PlacementServiceAddress, a.config.HostAddress, a.config.HeartbeatInterval)
	a.startDeactivationTicker(a.config.ActorDeactivationScanInterval)

	log.Infof("actor runtime started. actor idle timeout: %s. actor scan interval: %s",
		a.config.ActorIdleTimeout.String(), a.config.ActorDeactivationScanInterval.String())

	return nil
}

func (a *actors) constructCombinedActorKey(actorType, actorID string) string {
	return fmt.Sprintf("%s-%s", actorType, actorID)
}

func (a *actors) updateActorUsage(actorType, actorID string) {
	key := a.constructCombinedActorKey(actorType, actorID)
	a.actorLastUsedLock.Lock()
	defer a.actorLastUsedLock.Unlock()
	a.actorLastUsed[key] = time.Now()
}

func (a *actors) deactivateActor(actorType, actorID string) error {
	req := channel.InvokeRequest{
		Method:   fmt.Sprintf("actors/%s/%s", actorType, actorID),
		Metadata: map[string]string{http.HTTPVerb: http.Delete},
	}

	resp, err := a.appChannel.InvokeMethod(&req)
	if err != nil {
		return err
	}

	if a.getStatusCodeFromMetadata(resp.Metadata) != 200 {
		return errors.New("error from actor sdk")
	}

	return nil
}

func (a *actors) getStatusCodeFromMetadata(metadata map[string]string) int {
	code := metadata[http.HTTPStatusCode]
	if code != "" {
		statusCode, err := strconv.Atoi(code)
		if err == nil {
			return statusCode
		}
	}

	return 200
}

func (a *actors) getActorTypeAndIDFromKey(key string) (string, string) {
	arr := strings.Split(key, "-")
	return arr[0], arr[1]
}

func (a *actors) startDeactivationTicker(interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for t := range ticker.C {
			a.actorLastUsedLock.RLock()

			for actorKey, lastUsed := range a.actorLastUsed {
				durationPassed := t.Sub(lastUsed)
				if durationPassed >= a.config.ActorIdleTimeout {
					go func(actorKey string) {
						actorType, actorID := a.getActorTypeAndIDFromKey(actorKey)
						err := a.deactivateActor(actorType, actorID)
						if err != nil {
							log.Warnf("failed to deactivate actor %s: %s", actorKey, err)
						} else {
							a.activeActorsLocks.Delete(actorKey)

							a.actorLastUsedLock.Lock()
							delete(a.actorLastUsed, actorKey)
							a.actorLastUsedLock.Unlock()
						}
					}(actorKey)
				}
			}
			a.actorLastUsedLock.RUnlock()
		}
	}()
}

func (a *actors) Call(req *CallRequest) (*CallResponse, error) {
	targetActorAddress := a.lookupActorAddress(req.ActorType, req.ActorID)
	if targetActorAddress == "" {
		return nil, fmt.Errorf("error finding address for actor type %s with id %s", req.ActorType, req.ActorID)
	}

	if a.placementBlock {
		<-a.placementSignal
	}

	go a.updateActorUsage(req.ActorType, req.ActorID)

	var resp []byte
	var err error

	if a.isActorLocal(targetActorAddress, a.config.HostAddress, a.config.Port) {
		err := a.tryActivateActor(req.ActorType, req.ActorID)
		if err != nil {
			return nil, err
		}

		resp, err = a.callLocalActor(req.ActorType, req.ActorID, req.Method, req.Data)
	} else {
		resp, err = a.callRemoteActor(targetActorAddress, req.ActorType, req.ActorID, req.Method, req.Data)
	}

	if err != nil {
		return nil, err
	}

	return &CallResponse{
		Data: resp,
	}, nil
}

func (a *actors) callLocalActor(actorType, actorID, actorMethod string, data []byte) ([]byte, error) {
	key := a.constructActorStateKey(actorType, actorID)
	l, _ := a.activeActorsLocks.LoadOrStore(key, &sync.RWMutex{})
	lock := l.(*sync.RWMutex)
	lock.Lock()
	defer lock.Unlock()

	method := fmt.Sprintf("actors/%s/%s/%s", actorType, actorID, actorMethod)
	req := channel.InvokeRequest{
		Method:   method,
		Payload:  data,
		Metadata: map[string]string{http.HTTPVerb: http.Put},
	}

	resp, err := a.appChannel.InvokeMethod(&req)
	if err != nil {
		return nil, err
	}

	return resp.Data, nil
}

func (a *actors) callRemoteActor(targetAddress, actorType, actorID, actorMethod string, data []byte) ([]byte, error) {
	req := pb.CallActorEnvelope{
		ActorType: actorType,
		ActorID:   actorID,
		Method:    actorMethod,
		Data:      &any.Any{Value: data},
	}

	conn, err := a.grpcConnectionFn(targetAddress)
	if err != nil {
		return nil, err
	}

	client := pb.NewActionsClient(conn)
	resp, err := client.CallActor(context.Background(), &req)
	if err != nil {
		return nil, err
	}

	return resp.Data.Value, nil
}

func (a *actors) tryActivateActor(actorType, actorID string) error {
	// create or get a per actor lock
	key := a.constructCombinedActorKey(actorType, actorID)
	l, exists := a.activeActorsLocks.LoadOrStore(key, &sync.RWMutex{})

	if !exists {
		// perform actual activation with per actor lock
		lock := l.(*sync.RWMutex)
		lock.Lock()
		defer lock.Unlock()

		stateKey := a.constructActorStateKey(actorType, actorID)
		var data []byte

		if a.store != nil {
			resp, err := a.store.Get(&state.GetRequest{
				Key: stateKey,
			})
			if err == nil {
				data = resp.Data
			}
		}

		req := channel.InvokeRequest{
			Method:   fmt.Sprintf("actors/%s/%s", actorType, actorID),
			Metadata: map[string]string{http.HTTPVerb: http.Post},
			Payload:  data,
		}

		_, err := a.appChannel.InvokeMethod(&req)
		if err != nil {
			a.activeActorsLocks.Delete(key)
			return fmt.Errorf("error activating actor type %s with id %s: %s", actorType, actorID, err)
		}
	}

	return nil
}

func (a *actors) isActorLocal(targetActorAddress, hostAddress string, grpcPort int) bool {
	return strings.Contains(targetActorAddress, "localhost") || strings.Contains(targetActorAddress, "127.0.0.1") ||
		targetActorAddress == fmt.Sprintf("%s:%v", hostAddress, grpcPort)
}

func (a *actors) GetState(req *GetStateRequest) (*StateResponse, error) {
	key := a.constructActorStateKey(req.ActorType, req.ActorID)
	resp, err := a.store.Get(&state.GetRequest{
		Key: key,
	})
	if err != nil {
		return nil, err
	}

	return &StateResponse{
		Data: resp.Data,
	}, nil
}

func (a *actors) SaveState(req *SaveStateRequest) error {
	key := a.constructActorStateKey(req.ActorType, req.ActorID)
	err := a.store.Set(&state.SetRequest{
		Value: req.Data,
		Key:   key,
	})
	return err
}

func (a *actors) constructActorStateKey(actorType, actorID string) string {
	return fmt.Sprintf("%s-%s-%s", a.config.ActionsID, actorType, actorID)
}

func (a *actors) connectToPlacementService(placementAddress, hostAddress string, heartbeatInterval time.Duration) {
	log.Infof("actors: starting connection attempt to placement service at %s", placementAddress)
	stream := a.getPlacementClientPersistently(placementAddress, hostAddress)

	log.Infof("actors: established connection to placement service at %s", placementAddress)

	go func() {
		for {
			host := pb.Host{
				Name:     hostAddress,
				Load:     1,
				Entities: a.config.HostedActorTypes,
				Port:     int64(a.config.Port),
			}

			if stream != nil {
				if err := stream.Send(&host); err != nil {
					log.Error("actors: connection failure to placement service: retrying")
					stream = a.getPlacementClientPersistently(placementAddress, hostAddress)
				}
			}
			time.Sleep(heartbeatInterval)
		}
	}()

	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				log.Error("actors: connection failure to placement service: retrying")
				stream = a.getPlacementClientPersistently(placementAddress, hostAddress)
			}
			if resp != nil {
				a.onPlacementOrder(resp)
			}
		}
	}()
}

func (a *actors) getPlacementClientPersistently(placementAddress, hostAddress string) pb.PlacementService_ReportActionStatusClient {
	for {
		retryInterval := time.Millisecond * 250

		conn, err := grpc.Dial(placementAddress, grpc.WithInsecure())
		if err != nil {
			time.Sleep(retryInterval)
			continue
		}
		header := metadata.New(map[string]string{idHeader: hostAddress})
		ctx := metadata.NewOutgoingContext(context.Background(), header)
		client := pb.NewPlacementServiceClient(conn)
		stream, err := client.ReportActionStatus(ctx)
		if err != nil {
			time.Sleep(retryInterval)
			continue
		}

		return stream
	}
}

func (a *actors) onPlacementOrder(in *pb.PlacementOrder) {
	log.Infof("actors: placement order received: %s", in.Operation)

	// lock all incoming calls when an updated table arrives
	a.operationUpdateLock.Lock()
	defer a.operationUpdateLock.Unlock()

	switch in.Operation {
	case lockOperation:
		{
			a.blockPlacements()

			go func() {
				time.Sleep(time.Second * 5)
				a.unblockPlacements()
			}()
		}
	case unlockOperation:
		{
			a.unblockPlacements()
		}
	case updateOperation:
		{
			a.updatePlacements(in.Tables)
		}
	}
}

func (a *actors) blockPlacements() {
	a.placementSignal = make(chan struct{})
	a.placementBlock = true
}

func (a *actors) unblockPlacements() {
	if a.placementBlock {
		a.placementBlock = false
		close(a.placementSignal)
	}
}

func (a *actors) updatePlacements(in *pb.PlacementTables) {
	if in.Version != a.placementTables.Version {
		for k, v := range in.Entries {
			loadMap := map[string]*placement.Host{}
			for lk, lv := range v.LoadMap {
				loadMap[lk] = placement.NewHost(lv.Name, lv.Load, lv.Port)
			}
			c := placement.NewFromExisting(v.Hosts, v.SortedSet, loadMap)
			a.placementTables.Entries[k] = c
		}

		a.placementTables.Version = in.Version
		log.Info("actors: placement tables updated")
	}
}

func (a *actors) lookupActorAddress(actorType, actorID string) string {
	// read lock for table map
	a.placementTableLock.RLock()
	defer a.placementTableLock.RUnlock()

	t := a.placementTables.Entries[actorType]
	if t == nil {
		return ""
	}
	host, _ := t.GetHost(actorID)
	return fmt.Sprintf("%s:%v", host.Name, host.Port)
}
