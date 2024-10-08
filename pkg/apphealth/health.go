/*
Copyright 2022 The Dapr Authors
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

package apphealth

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/kit/logger"
)

const (
	AppStatusUnhealthy uint8 = 0
	AppStatusHealthy   uint8 = 1

	// reportMinInterval is the minimum interval between health reports.
	reportMinInterval = time.Second
)

var log = logger.NewLogger("dapr.apphealth")

// AppHealth manages the health checks for the app.
type AppHealth struct {
	config       config.AppHealthConfig
	probeFn      ProbeFunction
	changeCb     ChangeCallback
	report       chan uint8
	failureCount atomic.Int32
	queue        chan struct{}

	// lastReport is the last report as UNIX microseconds time.
	lastReport atomic.Int64

	clock   clock.WithTicker
	wg      sync.WaitGroup
	closed  atomic.Bool
	closeCh chan struct{}
}

// ProbeFunction is the signature of the function that performs health probes.
// Health probe functions return errors only in case of internal errors.
// Network errors are considered probe failures, and should return nil as errors.
type ProbeFunction func(context.Context) (bool, error)

// ChangeCallback is the signature of the callback that is invoked when the app's health status changes.
type ChangeCallback func(ctx context.Context, status uint8)

// New creates a new AppHealth object.
func New(config config.AppHealthConfig, probeFn ProbeFunction) *AppHealth {
	a := &AppHealth{
		config:  config,
		probeFn: probeFn,
		report:  make(chan uint8, 1),
		queue:   make(chan struct{}, 1),
		clock:   &clock.RealClock{},
		closeCh: make(chan struct{}),
	}

	// Initial state is unhealthy until we validate it
	a.failureCount.Store(config.Threshold)

	return a
}

// OnHealthChange sets the callback that is invoked when the health of the app changes (app becomes either healthy or unhealthy).
func (h *AppHealth) OnHealthChange(cb ChangeCallback) {
	h.changeCb = cb
}

// StartProbes starts polling the app on the interval.
func (h *AppHealth) StartProbes(ctx context.Context) error {
	if h.closed.Load() {
		return errors.New("app health is closed")
	}

	if h.probeFn == nil {
		return errors.New("cannot start probes with nil probe function")
	}
	if h.config.ProbeInterval <= 0 {
		return errors.New("probe interval must be larger than 0")
	}
	if h.config.ProbeTimeout > h.config.ProbeInterval {
		return errors.New("app health checks probe timeouts must be smaller than probe intervals")
	}

	log.Info("App health probes starting")

	ctx, cancel := context.WithCancel(ctx)

	h.wg.Add(2)
	go func() {
		defer h.wg.Done()
		defer cancel()
		select {
		case <-h.closeCh:
		case <-ctx.Done():
		}
	}()

	go func() {
		defer h.wg.Done()

		ticker := h.clock.NewTicker(h.config.ProbeInterval)
		ch := ticker.C()
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				log.Info("App health probes stopping")
				return
			case status := <-h.report:
				log.Debug("Received health status report")
				h.setResult(ctx, status == AppStatusHealthy)
			case <-ch:
				log.Debug("Probing app health")
				h.Enqueue()
			case <-h.queue:
				// Run synchronously so the loop is blocked
				h.doProbe(ctx)
			}
		}
	}()

	return nil
}

// Enqueue adds a new probe request to the queue
func (h *AppHealth) Enqueue() {
	// The queue has a capacity of 1, so no more than one iteration can be queued up
	select {
	case h.queue <- struct{}{}:
		// Do nothing
	default:
		// Do nothing
	}
	return
}

// ReportHealth is used by the runtime to report a health signal from the app.
func (h *AppHealth) ReportHealth(status uint8) {
	// If the user wants health probes only, short-circuit here
	if h.config.ProbeOnly {
		return
	}

	// Limit health reports to 1 per second
	if !h.ratelimitReports() {
		return
	}

	// Channel is buffered, so make sure that this doesn't block
	// Just in case another report is being worked on!
	select {
	case h.report <- status:
		// No action
	default:
		// No action
	}
}

// GetStatus returns the status of the app's health
func (h *AppHealth) GetStatus() uint8 {
	fc := h.failureCount.Load()
	if fc >= h.config.Threshold {
		return AppStatusUnhealthy
	}

	return AppStatusHealthy
}

// Performs a health probe.
// Should be invoked in a background goroutine.
func (h *AppHealth) doProbe(parentCtx context.Context) {
	ctx, cancel := context.WithTimeout(parentCtx, h.config.ProbeTimeout)
	defer cancel()

	successful, err := h.probeFn(ctx)
	if err != nil {
		h.setResult(parentCtx, false)
		log.Errorf("App health probe could not complete with error: %v", err)
		return
	}

	log.Debug("App health probe successful: " + strconv.FormatBool(successful))
	h.setResult(parentCtx, successful)
}

// Returns true if the health report can be saved. Only 1 report per second at most is allowed.
func (h *AppHealth) ratelimitReports() bool {
	var (
		swapped  bool
		attempts uint8
	)

	now := h.clock.Now().UnixMicro()

	// Attempts at most 2 times before giving up, as the report may be stale at that point
	for !swapped && attempts < 2 {
		attempts++

		// If the last report was less than `reportMinInterval` ago, nothing to do here
		prev := h.lastReport.Load()
		if prev > now-reportMinInterval.Microseconds() {
			return false
		}

		swapped = h.lastReport.CompareAndSwap(prev, now)
	}

	// If we couldn't do the swap after 2 attempts, just return false
	return swapped
}

func (h *AppHealth) setResult(ctx context.Context, successful bool) {
	h.lastReport.Store(h.clock.Now().UnixMicro())

	if successful {
		// Reset the failure count
		// If the previous value was >= threshold, we need to report a health change
		prev := h.failureCount.Swap(0)
		if prev >= h.config.Threshold {
			log.Info("App entered healthy status")
			if h.changeCb != nil {
				h.wg.Add(1)
				go func() {
					defer h.wg.Done()
					h.changeCb(ctx, AppStatusHealthy)
				}()
			}
		}
		return
	}

	// Count the failure
	failures := h.failureCount.Add(1)

	// First, check if we've overflown
	if failures < 0 {
		// Reset to the threshold + 1
		h.failureCount.Store(h.config.Threshold + 1)
	} else if failures == h.config.Threshold {
		// If we're here, we just passed the threshold right now
		log.Warn("App entered un-healthy status")
		if h.changeCb != nil {
			h.wg.Add(1)
			go func() {
				defer h.wg.Done()
				h.changeCb(ctx, AppStatusUnhealthy)
			}()
		}
	}
}

func (h *AppHealth) Close() error {
	defer h.wg.Wait()
	if h.closed.CompareAndSwap(false, true) {
		close(h.closeCh)
	}

	return nil
}
