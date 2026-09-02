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

package rendezvous

import (
	"slices"
	"strconv"
	"testing"

	"github.com/cespare/xxhash/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pin the HASH_ALGORITHM_RENDEZVOUS wire contract, which every
// party resolving actor ownership must implement identically. Changing any
// value here re-shards every actor in every running cluster, so intended
// algorithm changes need a new HashAlgorithm enum value instead.

var contractHosts = []string{"10.0.0.1:50002", "10.0.0.2:50002", "10.0.0.3:50002"}

// TestContractScore pins the score function, xxhash64("<host>||<key>"),
// with values derived independently of this package.
func TestContractScore(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		host string
		key  string
		want uint64
	}{
		{host: "10.0.0.1:50002", key: "actor-1", want: 11841101866786924059},
		{host: "10.0.0.2:50002", key: "actor-1", want: 14141808514106082977},
		{host: "10.0.0.3:50002", key: "actor-1", want: 1347952300613944576},
	} {
		assert.Equalf(t, test.want, score(test.host, test.key),
			"score(%q, %q) changed: this re-shards every actor in every cluster",
			test.host, test.key)
	}
}

// TestContractSeparator pins the separator as the two byte string "||", by
// asserting the score equals the hash of the concatenation built by hand.
func TestContractSeparator(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "||", separator)
	assert.Equal(t,
		xxhash.Sum64String("10.0.0.1:50002"+"||"+"actor-1"),
		score("10.0.0.1:50002", "actor-1"),
	)
}

// TestContractOwners pins the resolved owner for keys including the empty
// key, a unicode key, and a key containing the separator itself.
func TestContractOwners(t *testing.T) {
	t.Parallel()

	table := New(contractHosts)

	for _, test := range []struct {
		key  string
		want string
	}{
		{key: "actor-1", want: "10.0.0.2:50002"},
		{key: "actor-2", want: "10.0.0.3:50002"},
		{key: "actor-3", want: "10.0.0.1:50002"},
		{key: "actor-4", want: "10.0.0.1:50002"},
		{key: "actor-5", want: "10.0.0.3:50002"},
		{key: "0", want: "10.0.0.2:50002"},
		{key: "", want: "10.0.0.2:50002"},
		{key: "ολυ-unicode-κλειδί", want: "10.0.0.1:50002"},
		{key: "myactor||weird", want: "10.0.0.1:50002"},
	} {
		owner, ok := table.Lookup(test.key)
		require.Truef(t, ok, "no owner for key %q", test.key)
		assert.Equalf(t, test.want, owner,
			"owner of key %q changed: this re-shards actors across hosts", test.key)
	}
}

func referenceLookup(hosts []string, key string) (string, bool) {
	uniq := slices.Clone(hosts)
	slices.Sort(uniq)
	uniq = slices.Compact(uniq)
	if len(uniq) == 0 {
		return "", false
	}

	best := uniq[0]
	bestScore := xxhash.Sum64String(best + "||" + key)
	for _, host := range uniq[1:] {
		// Strictly greater: on a tie the earlier, lexicographically smaller
		// host keeps ownership.
		if s := xxhash.Sum64String(host + "||" + key); s > bestScore {
			best, bestScore = host, s
		}
	}

	return best, true
}

func TestContractMatchesReference(t *testing.T) {
	t.Parallel()

	for _, numHosts := range []int{1, 2, 3, 7, 32, 128} {
		t.Run(strconv.Itoa(numHosts)+"-hosts", func(t *testing.T) {
			t.Parallel()

			hostSet := hosts(numHosts)
			table := New(hostSet)

			for i := range 2000 {
				key := "actor-" + strconv.Itoa(i)
				want, wantOK := referenceLookup(hostSet, key)
				got, gotOK := table.Lookup(key)
				require.Equal(t, wantOK, gotOK)
				require.Equalf(t, want, got, "owner mismatch for key %q", key)
			}
		})
	}
}

func TestNewSortsAndCompacts(t *testing.T) {
	t.Parallel()

	table := New([]string{
		"10.0.0.3:50002",
		"10.0.0.1:50002",
		"10.0.0.2:50002",
		"10.0.0.1:50002",
		"10.0.0.3:50002",
	})

	assert.Equal(t, []string{
		"10.0.0.1:50002",
		"10.0.0.2:50002",
		"10.0.0.3:50002",
	}, table.Hosts())
	assert.True(t, slices.IsSorted(table.Hosts()))
}

// TestEqualNilReceiver covers the nil-table branch of Equal, which is reached
// whenever a placement table has not been installed for an actor type yet.
func TestEqualNilReceiver(t *testing.T) {
	t.Parallel()

	var nilTable *Table

	t.Run("nil equals nil", func(t *testing.T) {
		t.Parallel()
		var other *Table
		assert.True(t, nilTable.Equal(other))
	})

	t.Run("nil equals empty", func(t *testing.T) {
		t.Parallel()
		assert.True(t, nilTable.Equal(New(nil)))
		assert.True(t, New(nil).Equal(nilTable))
	})

	t.Run("nil does not equal populated", func(t *testing.T) {
		t.Parallel()
		assert.False(t, nilTable.Equal(New(contractHosts)))
		assert.False(t, New(contractHosts).Equal(nilTable))
	})
}

func TestHostsDoesNotRetainInput(t *testing.T) {
	t.Parallel()

	input := []string{"10.0.0.2:50002", "10.0.0.1:50002"}
	table := New(input)
	before := slices.Clone(table.Hosts())

	// Mutating the caller's slice must not change the table.
	input[0] = "10.0.0.9:50002"
	assert.Equal(t, before, table.Hosts())

	owner, ok := table.Lookup("actor-1")
	require.True(t, ok)
	assert.Contains(t, before, owner)
}
