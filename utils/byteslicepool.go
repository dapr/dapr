/*
Copyright 2021 The Dapr Authors
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

package utils

import (
	"sync"
)

/*
Originally based on https://github.com/xdg-go/zzz-slice-recycling
Copyright (C) 2019 by David A. Golden
License (Apache2): https://github.com/xdg-go/zzz-slice-recycling/blob/master/LICENSE
*/

// ByteSlicePool is a wrapper around sync.Pool to get []byte objects with a given capacity.
type ByteSlicePool struct {
	MinCap int
	pool   *sync.Pool
}

// NewByteSlicePool returns a new ByteSlicePool object.
func NewByteSlicePool(minCap int) *ByteSlicePool {
	return &ByteSlicePool{
		MinCap: minCap,
		pool:   &sync.Pool{},
	}
}

// Get a slice from the pool.
// The cap parameter is used only if we need to allocate a new byte slice; there's no guarantee a slice retrieved from the pool will have enough capacity for that.
func (sp ByteSlicePool) Get(cap int) []byte {
	bp := sp.pool.Get()
	if bp == nil {
		if cap < sp.MinCap {
			cap = sp.MinCap
		}
		return make([]byte, 0, cap)
	}
	buf := bp.([]byte)
	// This will be optimized by the compiler
	for i := range buf {
		buf[i] = 0
	}
	return buf[0:0]
}

// Put a slice back in the pool.
func (sp ByteSlicePool) Put(bs []byte) {
	// The linter here complains because we're putting a slice rather than a pointer in the pool.
	// The complain is valid, because doing so does cause an allocation for the local copy of the slice header.
	// However, this is ok for us because given how we use ByteSlicePool, we can't keep around the pointer we took out.
	// See this thread for some discussion: https://github.com/dominikh/go-tools/issues/1336
	//nolint:staticcheck
	sp.pool.Put(bs)
}

// Resize a byte slice, making sure that it has enough capacity for a given size.
func (sp ByteSlicePool) Resize(orig []byte, size int) []byte {
	if size < cap(orig) {
		return orig[0:size]
	}

	// Allocate a new byte slice and then discard the old one, too small, so it can be garbage collected
	temp := make([]byte, size, max(size, cap(orig)*2))
	copy(temp, orig)
	return temp
}

func max(x, y int) int {
	if x < y {
		return y
	}
	return x
}
