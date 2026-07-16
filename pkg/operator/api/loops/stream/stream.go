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

package stream

import (
	"context"
	"fmt"

	"github.com/dapr/dapr/pkg/operator/api/loops"
	"github.com/dapr/dapr/pkg/operator/api/loops/sender"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var (
	log = logger.NewLogger("dapr.operator.api.loops.stream")

	LoopFactory = loop.New[loops.EventStream](8)
)

type Options struct {
	Stream sender.Interface
}

type stream struct {
	sender sender.Interface
	loop   loop.Interface[loops.EventStream]
}

func New(opts Options) loop.Interface[loops.EventStream] {
	s := &stream{
		sender: opts.Stream,
	}
	s.loop = LoopFactory.NewLoop(s)
	return s.loop
}

func (s *stream) Handle(ctx context.Context, event loops.EventStream) error {
	switch e := event.(type) {
	case *loops.StreamSend:
		return s.handleSend(e)
	case *loops.Shutdown:
		s.handleShutdown(e)
	default:
		panic(fmt.Sprintf("unknown stream event type: %T", e))
	}

	return nil
}

func (s *stream) handleSend(e *loops.StreamSend) error {
	if err := s.sender.Send(e.Resource, e.EventType); err != nil {
		log.Warnf("error sending message: %s", err)
		return err
	}
	return nil
}

func (s *stream) handleShutdown(e *loops.Shutdown) {
	log.Debugf("stream loop shutdown: %v", e.Error)
}
