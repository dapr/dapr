// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package tag // import "go.mongodb.org/mongo-driver/tag"

// Tag is a name/vlaue pair.
type Tag struct {
	Name  string
	Value string
}

// NewTagSetFromMap creates a new tag set from a map.
func NewTagSetFromMap(m map[string]string) Set {
	var set Set
	for k, v := range m {
		set = append(set, Tag{Name: k, Value: v})
	}

	return set
}

// NewTagSetsFromMaps creates new tag sets from maps.
func NewTagSetsFromMaps(maps []map[string]string) []Set {
	sets := make([]Set, 0, len(maps))
	for _, m := range maps {
		sets = append(sets, NewTagSetFromMap(m))
	}
	return sets
}

// Set is an ordered list of Tags.
type Set []Tag

// Contains indicates whether the name/value pair exists in the tagset.
func (ts Set) Contains(name, value string) bool {
	for _, t := range ts {
		if t.Name == name && t.Value == value {
			return true
		}
	}

	return false
}

// ContainsAll indicates whether all the name/value pairs exist in the tagset.
func (ts Set) ContainsAll(other []Tag) bool {
	for _, ot := range other {
		if !ts.Contains(ot.Name, ot.Value) {
			return false
		}
	}

	return true
}
