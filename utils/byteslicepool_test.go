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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestByteSlicePool(t *testing.T) {
	minCap := 32
	pool := NewByteSlicePool(minCap)

	bs := pool.Get(minCap)
	assert.Equal(t, 0, len(bs))
	assert.Equal(t, minCap, cap(bs))

	pool.Put(bs)
	bs2 := pool.Get(minCap)
	assert.Equal(t, &bs, &bs2)
	assert.Equal(t, minCap, cap(bs2))

	for i := 0; i < minCap; i++ {
		bs2 = append(bs2, 0)
	}

	// Less than minCap
	// Capacity will not change after resize
	size2 := 16
	bs2 = pool.Resize(bs2, size2)
	assert.Equal(t, size2, len(bs2))
	assert.Equal(t, minCap, cap(bs2))

	// Less than twice the minCap
	// Will automatically expand to twice the original capacity
	size3 := 48
	bs2 = pool.Resize(bs2, size3)
	assert.Equal(t, size3, len(bs2))
	assert.Equal(t, minCap*2, cap(bs2))

	// More than twice the minCap
	// Will automatically expand to the specified size
	size4 := 128
	bs2 = pool.Resize(bs2, size4)
	assert.Equal(t, size4, len(bs2))
	assert.Equal(t, size4, cap(bs2))
}
