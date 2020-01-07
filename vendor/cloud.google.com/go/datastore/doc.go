// Copyright 2016 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/*
Package datastore provides a client for Google Cloud Datastore.

See https://godoc.org/cloud.google.com/go for authentication, timeouts,
connection pooling and similar aspects of this package.


Basic Operations

Entities are the unit of storage and are associated with a key. A key
consists of an optional parent key, a string application ID, a string kind
(also known as an entity type), and either a StringID or an IntID. A
StringID is also known as an entity name or key name.

It is valid to create a key with a zero StringID and a zero IntID; this is
called an incomplete key, and does not refer to any saved entity. Putting an
entity into the datastore under an incomplete key will cause a unique key
to be generated for that entity, with a non-zero IntID.

An entity's contents are a mapping from case-sensitive field names to values.
Valid value types are:
  - Signed integers (int, int8, int16, int32 and int64)
  - bool
  - string
  - float32 and float64
  - []byte (up to 1 megabyte in length)
  - Any type whose underlying type is one of the above predeclared types
  - *Key
  - GeoPoint
  - time.Time (stored with microsecond precision, retrieved as local time)
  - Structs whose fields are all valid value types
  - Pointers to structs whose fields are all valid value types
  - Slices of any of the above
  - Pointers to a signed integer, bool, string, float32, or float64

Slices of structs are valid, as are structs that contain slices.

The Get and Put functions load and save an entity's contents. An entity's
contents are typically represented by a struct pointer.

Example code:

	type Entity struct {
		Value string
	}

	func main() {
		ctx := context.Background()

		// Create a datastore client. In a typical application, you would create
		// a single client which is reused for every datastore operation.
		dsClient, err := datastore.NewClient(ctx, "my-project")
		if err != nil {
			// Handle error.
		}

		k := datastore.NameKey("Entity", "stringID", nil)
		e := new(Entity)
		if err := dsClient.Get(ctx, k, e); err != nil {
			// Handle error.
		}

		old := e.Value
		e.Value = "Hello World!"

		if _, err := dsClient.Put(ctx, k, e); err != nil {
			// Handle error.
		}

		fmt.Printf("Updated value from %q to %q\n", old, e.Value)
	}

GetMulti, PutMulti and DeleteMulti are batch versions of the Get, Put and
Delete functions. They take a []*Key instead of a *Key, and may return a
datastore.MultiError when encountering partial failure.

Mutate generalizes PutMulti and DeleteMulti to a sequence of any Datastore mutations.
It takes a series of mutations created with NewInsert, NewUpdate, NewUpsert and
NewDelete and applies them atomically.


Properties

An entity's contents can be represented by a variety of types. These are
typically struct pointers, but can also be any type that implements the
PropertyLoadSaver interface. If using a struct pointer, you do not have to
explicitly implement the PropertyLoadSaver interface; the datastore will
automatically convert via reflection. If a struct pointer does implement
PropertyLoadSaver then those methods will be used in preference to the default
behavior for struct pointers. Struct pointers are more strongly typed and are
easier to use; PropertyLoadSavers are more flexible.

The actual types passed do not have to match between Get and Put calls or even
across different calls to datastore. It is valid to put a *PropertyList and
get that same entity as a *myStruct, or put a *myStruct0 and get a *myStruct1.
Conceptually, any entity is saved as a sequence of properties, and is loaded
into the destination value on a property-by-property basis. When loading into
a struct pointer, an entity that cannot be completely represented (such as a
missing field) will result in an ErrFieldMismatch error but it is up to the
caller whether this error is fatal, recoverable or ignorable.

By default, for struct pointers, all properties are potentially indexed, and
the property name is the same as the field name (and hence must start with an
upper case letter).

Fields may have a `datastore:"name,options"` tag. The tag name is the
property name, which must be one or more valid Go identifiers joined by ".",
but may start with a lower case letter. An empty tag name means to just use the
field name. A "-" tag name means that the datastore will ignore that field.

The only valid options are "omitempty", "noindex" and "flatten".

If the options include "omitempty" and the value of the field is a zero value,
then the field will be omitted on Save. Zero values are best defined in the
golang spec (https://golang.org/ref/spec#The_zero_value). Struct field values
will never be empty, except for nil pointers.

If options include "noindex" then the field will not be indexed. All fields
are indexed by default. Strings or byte slices longer than 1500 bytes cannot
be indexed; fields used to store long strings and byte slices must be tagged
with "noindex" or they will cause Put operations to fail.

For a nested struct field, the options may also include "flatten". This
indicates that the immediate fields and any nested substruct fields of the
nested struct should be flattened. See below for examples.

To use multiple options together, separate them by a comma.
The order does not matter.

If the options is "" then the comma may be omitted.

Example code:

	// A and B are renamed to a and b.
	// A, C and J are not indexed.
	// D's tag is equivalent to having no tag at all (E).
	// I is ignored entirely by the datastore.
	// J has tag information for both the datastore and json packages.
	type TaggedStruct struct {
		A int `datastore:"a,noindex"`
		B int `datastore:"b"`
		C int `datastore:",noindex"`
		D int `datastore:""`
		E int
		I int `datastore:"-"`
		J int `datastore:",noindex" json:"j"`
	}


Slice Fields

A field of slice type corresponds to a Datastore array property, except for []byte, which corresponds
to a Datastore blob.

Zero-length slice fields are not saved. Slice fields of length 1 or greater are saved
as Datastore arrays. When a zero-length Datastore array is loaded into a slice field,
the slice field remains unchanged.

If a non-array value is loaded into a slice field, the result will be a slice with
one element, containing the value.

Loading Nulls

Loading a Datastore Null into a basic type (int, float, etc.) results in a zero value.
Loading a Null into a slice of basic type results in a slice of size 1 containing the zero value.
Loading a Null into a pointer field results in nil.
Loading a Null into a field of struct type is an error.

Pointer Fields

A struct field can be a pointer to a signed integer, floating-point number, string or
bool. Putting a non-nil pointer will store its dereferenced value. Putting a nil
pointer will store a Datastore Null property, unless the field is marked omitempty,
in which case no property will be stored.

Loading a Null into a pointer field sets the pointer to nil. Loading any other value
allocates new storage with the value, and sets the field to point to it.


Key Field

If the struct contains a *datastore.Key field tagged with the name "__key__",
its value will be ignored on Put. When reading the Entity back into the Go struct,
the field will be populated with the *datastore.Key value used to query for
the Entity.

Example code:

	type MyEntity struct {
		A int
		K *datastore.Key `datastore:"__key__"`
	}

	func main() {
		ctx := context.Background()
		dsClient, err := datastore.NewClient(ctx, "my-project")
		if err != nil {
			// Handle error.
		}

		k := datastore.NameKey("Entity", "stringID", nil)
		e := MyEntity{A: 12}
		if _, err := dsClient.Put(ctx, k, &e); err != nil {
			// Handle error.
		}

		var entities []MyEntity
		q := datastore.NewQuery("Entity").Filter("A =", 12).Limit(1)
		if _, err := dsClient.GetAll(ctx, q, &entities); err != nil {
			// Handle error
		}

		log.Println(entities[0])
		// Prints {12 /Entity,stringID}
	}



Structured Properties

If the struct pointed to contains other structs, then the nested or embedded
structs are themselves saved as Entity values. For example, given these definitions:

	type Inner struct {
		W int32
		X string
	}

	type Outer struct {
		I Inner
	}

then an Outer would have one property, Inner, encoded as an Entity value.

If an outer struct is tagged "noindex" then all of its implicit flattened
fields are effectively "noindex".

If the Inner struct contains a *Key field with the name "__key__", like so:

	type Inner struct {
		W int32
		X string
		K *datastore.Key `datastore:"__key__"`
	}

	type Outer struct {
		I Inner
	}

then the value of K will be used as the Key for Inner, represented
as an Entity value in datastore.

If any nested struct fields should be flattened, instead of encoded as
Entity values, the nested struct field should be tagged with the "flatten"
option. For example, given the following:

	type Inner1 struct {
		W int32
		X string
	}

	type Inner2 struct {
		Y float64
	}

	type Inner3 struct {
		Z bool
	}

	type Inner4 struct {
		WW int
	}

	type Inner5 struct {
		X Inner4
	}

	type Outer struct {
		A int16
		I []Inner1 `datastore:",flatten"`
		J Inner2   `datastore:",flatten"`
		K Inner5   `datastore:",flatten"`
		Inner3     `datastore:",flatten"`
	}

an Outer's properties would be equivalent to those of:

	type OuterEquivalent struct {
		A          int16
		IDotW      []int32  `datastore:"I.W"`
		IDotX      []string `datastore:"I.X"`
		JDotY      float64  `datastore:"J.Y"`
		KDotXDotWW int      `datastore:"K.X.WW"`
		Z          bool
	}

Note that the "flatten" option cannot be used for Entity value fields or
PropertyLoadSaver implementers. The server will reject any dotted field names
for an Entity value.


The PropertyLoadSaver Interface

An entity's contents can also be represented by any type that implements the
PropertyLoadSaver interface. This type may be a struct pointer, but it does
not have to be. The datastore package will call Load when getting the entity's
contents, and Save when putting the entity's contents.
Possible uses include deriving non-stored fields, verifying fields, or indexing
a field only if its value is positive.

Example code:

	type CustomPropsExample struct {
		I, J int
		// Sum is not stored, but should always be equal to I + J.
		Sum int `datastore:"-"`
	}

	func (x *CustomPropsExample) Load(ps []datastore.Property) error {
		// Load I and J as usual.
		if err := datastore.LoadStruct(x, ps); err != nil {
			return err
		}
		// Derive the Sum field.
		x.Sum = x.I + x.J
		return nil
	}

	func (x *CustomPropsExample) Save() ([]datastore.Property, error) {
		// Validate the Sum field.
		if x.Sum != x.I + x.J {
			return nil, errors.New("CustomPropsExample has inconsistent sum")
		}
		// Save I and J as usual. The code below is equivalent to calling
		// "return datastore.SaveStruct(x)", but is done manually for
		// demonstration purposes.
		return []datastore.Property{
			{
				Name:  "I",
				Value: int64(x.I),
			},
			{
				Name:  "J",
				Value: int64(x.J),
			},
		}, nil
	}

The *PropertyList type implements PropertyLoadSaver, and can therefore hold an
arbitrary entity's contents.

The KeyLoader Interface

If a type implements the PropertyLoadSaver interface, it may
also want to implement the KeyLoader interface.
The KeyLoader interface exists to allow implementations of PropertyLoadSaver
to also load an Entity's Key into the Go type. This type may be a struct
pointer, but it does not have to be. The datastore package will call LoadKey
when getting the entity's contents, after calling Load.

Example code:

	type WithKeyExample struct {
		I int
		Key   *datastore.Key
	}

	func (x *WithKeyExample) LoadKey(k *datastore.Key) error {
		x.Key = k
		return nil
	}

	func (x *WithKeyExample) Load(ps []datastore.Property) error {
		// Load I as usual.
		return datastore.LoadStruct(x, ps)
	}

	func (x *WithKeyExample) Save() ([]datastore.Property, error) {
		// Save I as usual.
		return datastore.SaveStruct(x)
	}

To load a Key into a struct which does not implement the PropertyLoadSaver
interface, see the "Key Field" section above.


Queries

Queries retrieve entities based on their properties or key's ancestry. Running
a query yields an iterator of results: either keys or (key, entity) pairs.
Queries are re-usable and it is safe to call Query.Run from concurrent
goroutines. Iterators are not safe for concurrent use.

Queries are immutable, and are either created by calling NewQuery, or derived
from an existing query by calling a method like Filter or Order that returns a
new query value. A query is typically constructed by calling NewQuery followed
by a chain of zero or more such methods. These methods are:
  - Ancestor and Filter constrain the entities returned by running a query.
  - Order affects the order in which they are returned.
  - Project constrains the fields returned.
  - Distinct de-duplicates projected entities.
  - KeysOnly makes the iterator return only keys, not (key, entity) pairs.
  - Start, End, Offset and Limit define which sub-sequence of matching entities
    to return. Start and End take cursors, Offset and Limit take integers. Start
    and Offset affect the first result, End and Limit affect the last result.
    If both Start and Offset are set, then the offset is relative to Start.
    If both End and Limit are set, then the earliest constraint wins. Limit is
    relative to Start+Offset, not relative to End. As a special case, a
    negative limit means unlimited.

Example code:

	type Widget struct {
		Description string
		Price       int
	}

	func printWidgets(ctx context.Context, client *datastore.Client) {
		q := datastore.NewQuery("Widget").
			Filter("Price <", 1000).
			Order("-Price")

		t := client.Run(ctx, q)
		for {
			var x Widget
			key, err := t.Next(&x)
			if err == iterator.Done {
				break
			}
			if err != nil {
				// Handle error.
			}
			fmt.Printf("Key=%v\nWidget=%#v\n\n", key, x)
		}
	}


Transactions

Client.RunInTransaction runs a function in a transaction.

Example code:

	type Counter struct {
		Count int
	}

	func incCount(ctx context.Context, client *datastore.Client) {
		var count int
		key := datastore.NameKey("Counter", "singleton", nil)
		_, err := client.RunInTransaction(ctx, func(tx *datastore.Transaction) error {
			var x Counter
			if err := tx.Get(key, &x); err != nil && err != datastore.ErrNoSuchEntity {
				return err
			}
			x.Count++
			if _, err := tx.Put(key, &x); err != nil {
				return err
			}
			count = x.Count
			return nil
		})
		if err != nil {
			// Handle error.
		}
		// The value of count is only valid once the transaction is successful
		// (RunInTransaction has returned nil).
		fmt.Printf("Count=%d\n", count)
	}

Pass the ReadOnly option to RunInTransaction if your transaction is used only for Get,
GetMulti or queries. Read-only transactions are more efficient.

Google Cloud Datastore Emulator

This package supports the Cloud Datastore emulator, which is useful for testing and
development. Environment variables are used to indicate that datastore traffic should be
directed to the emulator instead of the production Datastore service.

To install and set up the emulator and its environment variables, see the documentation
at https://cloud.google.com/datastore/docs/tools/datastore-emulator.
*/
package datastore // import "cloud.google.com/go/datastore"
