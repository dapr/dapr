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

// Package leader is the placement connector which dials the current scheduler
// placement leader, discovered from the scheduler WatchHosts stream. When the
// leader changes, the current connection is closed so the placement client
// reconnects to the new leader through its usual reconnect machinery.
package leader

import (
	"context"
	"errors"
	"sync"

	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors/internal/placement/connector"
	"github.com/dapr/dapr/pkg/runtime/scheduler/leadership"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement.connector.leader")

// ErrSchedulerPlacementUnsupported is returned by Connect when the connected
// scheduler cluster does not advertise a placement leader, i.e. no scheduler
// serves placement (old schedulers or placement disabled).
var ErrSchedulerPlacementUnsupported = errors.New("scheduler cluster does not support placement")

type Options struct {
	Leadership  *leadership.Leadership
	GRPCOptions []grpc.DialOption
}

type leader struct {
	leadership *leadership.Leadership
	gopts      []grpc.DialOption

	lock         sync.Mutex
	conn         *grpc.ClientConn
	addr         string
	watchStarted bool
}

func New(opts Options) connector.Interface {
	return &leader{
		leadership: opts.Leadership,
		gopts:      opts.GRPCOptions,
	}
}

func (l *leader) Connect(ctx context.Context) (*grpc.ClientConn, error) {
	// Close any previous connection before dialing anew.
	l.lock.Lock()
	if l.conn != nil {
		l.conn.Close()
		l.conn = nil
	}
	l.lock.Unlock()

	for {
		addr, unsupported, changed := l.leadership.Leader()
		if unsupported {
			return nil, ErrSchedulerPlacementUnsupported
		}

		if addr == "" {
			// Waiting is deliberate, even while every scheduler is
			// unreachable, since falling back on unreachability alone could
			// choose a different placement authority than the rest of the
			// cluster. Failing closed until restart is what preserves single
			// activation across a rollback.
			log.Debug("Waiting for a scheduler placement leader to be advertised")
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-changed:
			}
			continue
		}

		log.Debugf("Attempting to connect to scheduler placement leader %s", addr)

		//nolint:staticcheck
		conn, err := grpc.DialContext(ctx, addr, l.gopts...)
		if err != nil {
			return nil, err
		}

		// The leader may have moved while dialling.
		if current, _, _ := l.leadership.Leader(); current != addr {
			conn.Close()
			continue
		}

		l.lock.Lock()
		l.conn = conn
		l.addr = addr
		if !l.watchStarted {
			l.watchStarted = true
			go l.watchLeader(ctx)
		}
		l.lock.Unlock()

		log.Infof("Connected to scheduler placement leader %s", addr)

		return conn, nil
	}
}

// watchLeader closes the current connection whenever the placement leader
// moves away from the connected address, forcing the placement client to
// reconnect to the new leader.
func (l *leader) watchLeader(ctx context.Context) {
	for {
		addr, unsupported, changed := l.leadership.Leader()

		l.lock.Lock()
		if l.conn != nil && l.addr != "" && (unsupported || addr != l.addr) {
			log.Infof("Scheduler placement leader changed from %s to %q, closing connection", l.addr, addr)
			l.conn.Close()
			l.conn = nil
		}
		l.lock.Unlock()

		select {
		case <-ctx.Done():
			l.lock.Lock()
			if l.conn != nil {
				l.conn.Close()
				l.conn = nil
			}
			l.lock.Unlock()
			return
		case <-changed:
		}
	}
}

func (l *leader) Address() string {
	l.lock.Lock()
	defer l.lock.Unlock()
	return l.addr
}
