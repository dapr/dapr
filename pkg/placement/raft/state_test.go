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

package raft

import (
	"testing"

	"github.com/stretchr/testify/require"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
)

func TestNewDaprHostMemberState(t *testing.T) {
	// act
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	require.Equal(t, uint64(0), s.Index())
	require.Equal(t, 0, s.NamespaceCount())
	require.Equal(t, 0, s.MemberCount())
}

func TestClone(t *testing.T) {
	// arrange
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})
	s.upsertMember(&DaprHostMember{
		Name:      "127.0.0.1:8080",
		Namespace: "ns1",
		AppID:     "FakeID",
		Entities:  []string{"actorTypeOne", "actorTypeTwo"},
	})

	// act
	newState := s.clone()

	require.NotSame(t, s, newState)
	table, err := newState.hashingTableMap("ns1")
	require.NoError(t, err)
	require.Nil(t, table)
	require.Equal(t, s.Index(), newState.Index())

	members, err := s.members("ns1")
	require.NoError(t, err)
	clonedMembers, err := newState.members("ns1")
	require.NoError(t, err)
	require.EqualValues(t, members, clonedMembers)
}

func TestUpsertRemoveMembers(t *testing.T) {
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})
	hostMember := &DaprHostMember{
		Name:      "127.0.0.1:8080",
		Namespace: "ns1",
		AppID:     "FakeID",
		Entities:  []string{"actorTypeOne", "actorTypeTwo"},
		UpdatedAt: 1,
	}

	updated := s.upsertMember(hostMember)

	m, err := s.members("ns1")
	require.NoError(t, err)
	ht, err := s.hashingTableMap("ns1")
	require.NoError(t, err)
	require.Len(t, m, 1)
	require.Len(t, m["127.0.0.1:8080"].Entities, 2)
	require.Len(t, ht, 2)
	require.True(t, updated)

	// An existing host starts serving new actor types
	hostMember.Entities = []string{"actorTypeThree"}
	updated = s.upsertMember(hostMember)

	require.True(t, updated)

	m, err = s.members("ns1")
	require.NoError(t, err)
	require.Len(t, m, 1)
	require.Len(t, m["127.0.0.1:8080"].Entities, 1)

	ht, err = s.hashingTableMap("ns1")
	require.NoError(t, err)
	require.Len(t, ht, 1)

	updated = s.removeMember(hostMember)

	m, err = s.members("ns1")
	require.NoError(t, err)
	require.Empty(t, m)
	require.True(t, updated)

	ht, err = s.hashingTableMap("ns1")
	require.NoError(t, err)
	require.Empty(t, ht)

	updated = s.removeMember(&DaprHostMember{
		Name: "127.0.0.1:8080",
	})
	require.False(t, updated)
}

func TestUpsertMemberNoHashingTable(t *testing.T) {
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	updated := s.upsertMember(&DaprHostMember{
		Name:      "127.0.0.1:8081",
		Namespace: "ns1",
		AppID:     "FakeID_2",
		Entities:  []string{"actorTypeOne", "actorTypeTwo", "actorTypeThree"},
		UpdatedAt: 1,
	})

	m, err := s.members("ns1")
	require.NoError(t, err)
	ht, err := s.hashingTableMap("ns1")
	require.NoError(t, err)
	require.Len(t, m, 1)
	require.Len(t, ht, 3)
	require.True(t, updated)

	updated = s.upsertMember(&DaprHostMember{
		Name:      "127.0.0.1:8081",
		Namespace: "ns1",
		AppID:     "FakeID_2",
		Entities:  []string{"actorTypeOne", "actorTypeTwo", "actorTypeThree"},
		UpdatedAt: 2,
	})

	require.False(t, updated)
}

func TestUpsertMemberNonActorHost(t *testing.T) {
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	testMember := &DaprHostMember{
		Name:      "127.0.0.1:8080",
		Namespace: "ns1",
		AppID:     "FakeID",
		Entities:  []string{},
		UpdatedAt: 100,
	}

	// act
	updated := s.upsertMember(testMember)
	require.False(t, updated)
}

func TestUpsertMemberEmptyEntities(t *testing.T) {
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	testMember := &DaprHostMember{
		Name:      "127.0.0.1:8080",
		Namespace: "ns1",
		AppID:     "FakeID",
		Entities:  []string{"a", "b"},
		UpdatedAt: 100,
	}

	updated := s.upsertMember(testMember)
	require.True(t, updated)

	testMember = &DaprHostMember{
		Name:      "127.0.0.1:8080",
		Namespace: "ns1",
		AppID:     "FakeID",
		Entities:  []string{},
		UpdatedAt: 100,
	}

	updated = s.upsertMember(testMember)
	require.True(t, updated)
}

func TestUpdateHashingTable(t *testing.T) {
	// each subtest has dependency on the state

	// arrange
	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	t.Run("add new hashing table per actor types", func(t *testing.T) {
		testMember := &DaprHostMember{
			Name:      "127.0.0.1:8080",
			Namespace: "ns1",
			AppID:     "FakeID",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
		}

		// act
		s.updateHashingTables(testMember)

		ht, err := s.hashingTableMap("ns1")
		require.NoError(t, err)
		require.Len(t, ht, 2)
		require.NotNil(t, ht["actorTypeOne"])
		require.NotNil(t, ht["actorTypeTwo"])
	})

	t.Run("update new hashing table per actor types", func(t *testing.T) {
		testMember := &DaprHostMember{
			Name:      "127.0.0.1:8081",
			Namespace: "ns1",
			AppID:     "FakeID",
			Entities:  []string{"actorTypeOne", "actorTypeTwo", "actorTypeThree"},
		}

		s.updateHashingTables(testMember)

		ht, err := s.hashingTableMap("ns1")
		require.NoError(t, err)
		require.Len(t, ht, 3)
		require.NotNil(t, ht["actorTypeOne"])
		require.NotNil(t, ht["actorTypeTwo"])
		require.NotNil(t, ht["actorTypeThree"])
	})
}

func TestRemoveHashingTable(t *testing.T) {
	testMember := &DaprHostMember{
		Name:      "fakeName",
		Namespace: "ns1",
		AppID:     "fakeID",
		Entities:  []string{"actorTypeOne", "actorTypeTwo"},
	}

	testcases := []struct {
		name       string
		totalTable int
	}{
		{"127.0.0.1:8080", 2},
		{"127.0.0.1:8081", 0},
	}

	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})
	for _, tc := range testcases {
		testMember.Name = tc.name
		s.updateHashingTables(testMember)
	}

	for _, tc := range testcases {
		t.Run("remove host "+tc.name, func(t *testing.T) {
			testMember.Name = tc.name
			s.removeHashingTables(testMember)

			ht, err := s.hashingTableMap("ns1")
			require.NoError(t, err)
			require.Len(t, ht, tc.totalTable)
		})
	}
}

func TestRestoreHashingTables(t *testing.T) {
	testnames := []string{
		"127.0.0.1:8080",
		"127.0.0.1:8081",
	}

	s := newDaprHostMemberState(DaprHostMemberStateConfig{
		replicationFactor: 10,
		minAPILevel:       0,
		maxAPILevel:       100,
	})

	s.data.Namespace = make(map[string]*daprNamespace)
	s.data.Namespace["ns1"] = &daprNamespace{
		Members: make(map[string]*DaprHostMember),
	}

	for _, tn := range testnames {
		s.lock.Lock()
		s.data.Namespace["ns1"].Members[tn] = &DaprHostMember{
			Name:      tn,
			Namespace: "ns1",
			AppID:     "fakeID",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
		}
		s.lock.Unlock()
	}
	ht, err := s.hashingTableMap("ns1")
	require.NoError(t, err)
	require.Empty(t, ht)

	s.restoreHashingTables()

	ht, err = s.hashingTableMap("ns1")
	require.NoError(t, err)
	require.Len(t, ht, 2)
}

func TestUpdateAPILevel(t *testing.T) {
	t.Run("no min nor max api levels arguments", func(t *testing.T) {
		s := newDaprHostMemberState(DaprHostMemberStateConfig{
			replicationFactor: 10,
		})
		m1 := &DaprHostMember{
			Name:      "127.0.0.1:8080",
			Namespace: "ns1",
			AppID:     "FakeID1",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 1,
			APILevel:  10,
		}
		m2 := &DaprHostMember{
			Name:      "127.0.0.1:8081",
			Namespace: "ns1",
			AppID:     "FakeID2",
			Entities:  []string{"actorTypeThree", "actorTypeFour"},
			UpdatedAt: 2,
			APILevel:  20,
		}
		m3 := &DaprHostMember{
			Name:      "127.0.0.1:8082",
			Namespace: "ns2",
			AppID:     "FakeID3",
			Entities:  []string{"actorTypeFive"},
			UpdatedAt: 3,
			APILevel:  30,
		}

		s.upsertMember(m1)
		require.Equal(t, uint32(10), s.data.APILevel)

		s.upsertMember(m2)
		require.Equal(t, uint32(10), s.data.APILevel)

		s.upsertMember(m3)
		require.Equal(t, uint32(10), s.data.APILevel)

		s.removeMember(m1)
		require.Equal(t, uint32(20), s.data.APILevel)

		s.removeMember(m2)
		require.Equal(t, uint32(30), s.data.APILevel)

		// TODO @elena - do we want to keep the cluster's last known api level, when all members are removed?
		// That's what we do currently, but I wonder why it's the case
		s.removeMember(m3)
		require.Equal(t, uint32(30), s.data.APILevel)
	})

	t.Run("min api levels set", func(t *testing.T) {
		s := newDaprHostMemberState(DaprHostMemberStateConfig{
			replicationFactor: 10,
			minAPILevel:       20,
			maxAPILevel:       100,
		})
		m1 := &DaprHostMember{
			Name:      "127.0.0.1:8080",
			Namespace: "ns1",
			AppID:     "FakeID1",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 1,
			APILevel:  10,
		}
		m2 := &DaprHostMember{
			Name:      "127.0.0.1:8081",
			Namespace: "ns2",
			AppID:     "FakeID1",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 2,
			APILevel:  30,
		}

		s.upsertMember(m1)
		require.Equal(t, uint32(20), s.data.APILevel)

		s.upsertMember(m2)
		require.Equal(t, uint32(20), s.data.APILevel)

		s.removeMember(m1)
		require.Equal(t, uint32(30), s.data.APILevel)
	})

	t.Run("max api levels set", func(t *testing.T) {
		s := newDaprHostMemberState(DaprHostMemberStateConfig{
			replicationFactor: 10,
			minAPILevel:       0,
			maxAPILevel:       20,
		})
		s.upsertMember(&DaprHostMember{
			Name:      "127.0.0.1:8080",
			Namespace: "ns1",
			AppID:     "FakeID1",
			Entities:  []string{"actorTypeOne", "actorTypeTwo"},
			UpdatedAt: 1,
			APILevel:  30,
		})

		require.Equal(t, uint32(20), s.data.APILevel)
	})
}

func TestDaprHostMemberState_UpsertRequired(t *testing.T) {
	type fields struct {
		data DaprHostMemberStateData
	}
	type args struct {
		ns  string
		new *placementv1pb.Host
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   bool
	}{
		{
			name: "yes - basic",
			fields: fields{
				data: DaprHostMemberStateData{
					Namespace: map[string]*daprNamespace{
						"ns": {
							Members: map[string]*DaprHostMember{
								"m1": {
									Name:      "m1",
									AppID:     "app1",
									Namespace: "ns",
									Entities:  []string{"a", "b", "c"},
								},
							},
						},
					},
				},
			},
			args: args{
				ns: "ns",
				new: &placementv1pb.Host{
					Name:      "m1",
					Id:        "app1",
					Namespace: "ns",
					Entities:  []string{"a", "b"},
				},
			},
			want: true,
		},
		{
			name: "no - basic",
			fields: fields{
				data: DaprHostMemberStateData{
					Namespace: map[string]*daprNamespace{
						"ns": {
							Members: map[string]*DaprHostMember{
								"m1": {
									Name:      "m1",
									AppID:     "app1",
									Namespace: "ns",
									Entities:  []string{"a", "b"},
								},
							},
						},
					},
				},
			},
			args: args{
				ns: "ns",
				new: &placementv1pb.Host{
					Name:      "m1",
					Id:        "app1",
					Namespace: "ns",
					Entities:  []string{"a", "b"},
				},
			},
			want: false,
		},
		{
			name: "yes - set empty",
			fields: fields{
				data: DaprHostMemberStateData{
					Namespace: map[string]*daprNamespace{
						"ns": {
							Members: map[string]*DaprHostMember{
								"m1": {
									Name:      "m1",
									AppID:     "app1",
									Namespace: "ns",
									Entities:  []string{"a", "b", "c"},
								},
							},
						},
					},
				},
			},
			args: args{
				ns: "ns",
				new: &placementv1pb.Host{
					Name:      "m1",
					Id:        "app1",
					Namespace: "ns",
					Entities:  []string{},
				},
			},
			want: true,
		},
		{
			name: "no - set empty",
			fields: fields{
				data: DaprHostMemberStateData{
					Namespace: map[string]*daprNamespace{
						"ns": {
							Members: map[string]*DaprHostMember{
								"m1": {
									Name:      "m1",
									AppID:     "app1",
									Namespace: "ns",
									Entities:  []string{},
								},
							},
						},
					},
				},
			},
			args: args{
				ns: "ns",
				new: &placementv1pb.Host{
					Name:      "m1",
					Id:        "app1",
					Namespace: "ns",
					Entities:  []string{},
				},
			},
			want: false,
		},
		{
			name: "no - set empty nil",
			fields: fields{
				data: DaprHostMemberStateData{
					Namespace: map[string]*daprNamespace{
						"ns": {
							Members: map[string]*DaprHostMember{
								"m1": {
									Name:      "m1",
									AppID:     "app1",
									Namespace: "ns",
									Entities:  []string{},
								},
							},
						},
					},
				},
			},
			args: args{
				ns: "ns",
				new: &placementv1pb.Host{
					Name:      "m1",
					Id:        "app1",
					Namespace: "ns",
					Entities:  nil,
				},
			},
			want: false,
		},
		{
			name: "no - set empty nil 2",
			fields: fields{
				data: DaprHostMemberStateData{
					Namespace: map[string]*daprNamespace{
						"ns": {
							Members: map[string]*DaprHostMember{
								"m1": {
									Name:      "m1",
									AppID:     "app1",
									Namespace: "ns",
									Entities:  nil,
								},
							},
						},
					},
				},
			},
			args: args{
				ns: "ns",
				new: &placementv1pb.Host{
					Name:      "m1",
					Id:        "app1",
					Namespace: "ns",
					Entities:  []string{},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &DaprHostMemberState{
				data: tt.fields.data,
			}
			if got := s.UpsertRequired(tt.args.ns, tt.args.new); got != tt.want {
				t.Errorf("DaprHostMemberState.UpsertRequired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasMember(t *testing.T) {
	// Initialize the state
	state := &DaprHostMemberState{}
	state.data.Namespace = make(map[string]*daprNamespace)

	// Add a namespace and a member
	state.data.Namespace["namespace1"] = &daprNamespace{
		Members: map[string]*DaprHostMember{
			"member1": {Name: "member1", AppID: "app1"},
		},
	}

	tests := []struct {
		name     string
		ns       string
		host     *placementv1pb.Host
		expected bool
	}{
		{
			name:     "Namespace does not exist",
			ns:       "nonexistent",
			host:     &placementv1pb.Host{Name: "member1", Id: "app1"},
			expected: false,
		},
		{
			name:     "Member does not exist",
			ns:       "namespace1",
			host:     &placementv1pb.Host{Name: "member2", Id: "app1", Namespace: "ns1"},
			expected: false,
		},
		{
			name:     "Member exists with matching details",
			ns:       "namespace1",
			host:     &placementv1pb.Host{Name: "member1", Id: "app1", Namespace: "ns1"},
			expected: true,
		},
		{
			name:     "Member exists with non-matching details",
			ns:       "namespace1",
			host:     &placementv1pb.Host{Name: "member1", Id: "app2", Namespace: "ns1"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := state.HasMember(tt.ns, tt.host)
			require.Equal(t, tt.expected, result)
		})
	}
}
