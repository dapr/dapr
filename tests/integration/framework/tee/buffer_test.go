/*
Copyright 2024 The Dapr Authors
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

package tee

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufio(t *testing.T) {
	pr, pw := io.Pipe()
	b := Buffer(t, pr)
	t.Cleanup(func() { require.NoError(t, pw.Close()) })

	r1 := b.Add(t)
	r2 := b.Add(t)

	m, err := pw.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, m)

	resp := make([]byte, 1024)
	n, err := r1.Read(resp)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "hello", string(resp[:n]))

	resp = make([]byte, 2)
	n, err = r2.Read(resp)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, "he", string(resp[:n]))
	n, err = r2.Read(resp)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, "ll", string(resp[:n]))
	n, err = r2.Read(resp)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "o", string(resp[:n]))

	pw.Write([]byte("world"))
	require.NoError(t, pw.Close())

	resp = make([]byte, 1024)
	n, err = r1.Read(resp)
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.Equal(t, "world", string(resp[:n]))

	all, err := io.ReadAll(r1)
	require.NoError(t, err)
	assert.Empty(t, all)

	all, err = io.ReadAll(r2)
	require.NoError(t, err)
	assert.Equal(t, "world", string(all))

	r3 := b.Add(t)
	all, err = io.ReadAll(r3)
	require.NoError(t, err)
	assert.Equal(t, "helloworld", string(all))

	for _, r := range []io.Reader{r1, r2, r3} {
		n, rerr := r.Read(resp)
		require.ErrorIs(t, rerr, io.EOF)
		assert.Equal(t, 0, n)
	}

	_, err = pw.Write([]byte("dapr"))
	require.ErrorIs(t, err, io.ErrClosedPipe)
}
