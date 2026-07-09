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

// Package fake provides an in-memory payloadstore.Store for tests. It is
// also the reference implementation of the Store contract: content-
// addressed keys, idempotent Put, and mandatory checksum verification on
// Get. It is never wired into the runtime.
package fake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/dapr/dapr/pkg/runtime/wfengine/payloadstore"
)

type Fake struct {
	lock      sync.Mutex
	data      map[string][]byte
	putCalls  int
	threshold int

	putFn func(ctx context.Context, instanceID string, data []byte) (payloadstore.Reference, error)
	getFn func(ctx context.Context, ref payloadstore.Reference) ([]byte, error)
}

func New() *Fake {
	f := &Fake{
		data:      make(map[string][]byte),
		threshold: payloadstore.DefaultThreshold,
	}
	f.putFn = f.defaultPut
	f.getFn = f.defaultGet
	return f
}

// WithThreshold sets the size in bytes at or above which ShouldOffload
// reports true.
func (f *Fake) WithThreshold(threshold int) *Fake {
	f.threshold = threshold
	return f
}

func (f *Fake) WithPutFn(fn func(ctx context.Context, instanceID string, data []byte) (payloadstore.Reference, error)) *Fake {
	f.putFn = fn
	return f
}

func (f *Fake) WithGetFn(fn func(ctx context.Context, ref payloadstore.Reference) ([]byte, error)) *Fake {
	f.getFn = fn
	return f
}

func (f *Fake) ShouldOffload(size int) bool {
	return size >= f.threshold
}

func (f *Fake) Put(ctx context.Context, instanceID string, data []byte) (payloadstore.Reference, error) {
	f.lock.Lock()
	f.putCalls++
	f.lock.Unlock()
	return f.putFn(ctx, instanceID, data)
}

func (f *Fake) Get(ctx context.Context, ref payloadstore.Reference) ([]byte, error) {
	return f.getFn(ctx, ref)
}

// PutCalls returns how many times Put has been invoked, including calls
// served by an injected putFn.
func (f *Fake) PutCalls() int {
	f.lock.Lock()
	defer f.lock.Unlock()
	return f.putCalls
}

// Tamper overwrites the payload stored under key without updating any
// reference, so a subsequent Get through a reference with the original
// checksum must fail verification.
func (f *Fake) Tamper(key string, data []byte) {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.data[key] = data
}

func (f *Fake) defaultPut(_ context.Context, _ string, data []byte) (payloadstore.Reference, error) {
	checksum := sha256.Sum256(data)
	key := hex.EncodeToString(checksum[:])

	f.lock.Lock()
	defer f.lock.Unlock()
	stored := make([]byte, len(data))
	copy(stored, data)
	f.data[key] = stored

	return payloadstore.Reference{
		Checksum: checksum,
		Key:      key,
		Size:     uint64(len(data)),
	}, nil
}

func (f *Fake) defaultGet(_ context.Context, ref payloadstore.Reference) ([]byte, error) {
	f.lock.Lock()
	data, ok := f.data[ref.Key]
	f.lock.Unlock()
	if !ok {
		return nil, fmt.Errorf("payload not found for key '%s'", ref.Key)
	}

	if sha256.Sum256(data) != ref.Checksum {
		return nil, fmt.Errorf("payload checksum mismatch for key '%s': stored data does not match reference checksum", ref.Key)
	}

	// Defensive copy: callers own the returned slice and must not be able
	// to mutate the stored payload through it.
	out := make([]byte, len(data))
	copy(out, data)
	return out, nil
}
