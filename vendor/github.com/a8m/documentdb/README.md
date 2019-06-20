## DocumentDB Go [![Build status][travis-image]][travis-url]

> Go driver for Microsoft Azure DocumentDB

### Note

This is a **WIP** project.  
I'm doing it on my spare time and hope to stabilize it soon. if you want to contribute, feel free to take some task [here](https://github.com/a8m/documentdb-go/issues/3)

## Table of contents:

* [Get Started](#get-started)
* [Examples](#examples)
* [Databases](#databases)
  * [Get](#readdatabase)
  * [Query](#querydatabases)
  * [List](#readdatabases)
  * [Create](#createdatabase)
  * [Replace](#replacedatabase)
  * [Delete](#deletedatabase)
* [Collections](#collections)
  * [Get](#readcollection)
  * [Query](#querycollections)
  * [List](#readcollection)
  * [Create](#createcollection)
  * [Delete](#deletecollection)
* [Documents](#documents)
  * [Get](#readdocument)
  * [Query](#querydocuments)
  * [List](#readdocuments)
  * [Create](#createdocument)
  * [Replace](#replacedocument)
  * [Delete](#deletedocument)
* [StoredProcedures](#storedprocedures)
  * [Get](#readstoredprocedure)
  * [Query](#querystoredprocedures)
  * [List](#readstoredprocedures)
  * [Create](#createstoredprocedure)
  * [Replace](#replacestoredprocedure)
  * [Delete](#deletestoredprocedure)
  * [Execute](#executestoredprocedure)
* [UserDefinedFunctions](#userdefinedfunctions)
  * [Get](#readuserdefinedfunction)
  * [Query](#queryuserdefinedfunctions)
  * [List](#readuserdefinedfunctions)
  * [Create](#createuserdefinedfunction)
  * [Replace](#replaceuserdefinedfunction)
  * [Delete](#deleteuserdefinedfunction)

### Get Started

#### Installation

```sh
$ go get github.com/a8m/documentdb-go
```

#### Add to your project

```go
import (
	"github.com/a8m/documentdb"
)

func main() {
	client := documentdb.New("connection-url", documentdb.Config{"master-key"})
	// Start using DocumentDB
	dbs, err := client.ReadDatabases()
	if err != nil {
	  log.Fatal(err)
	}
	fmt.Println(dbs)
}
```

### Databases

#### ReadDatabase

```go
func main() {
	// ...
	db, err := client.ReadDatabase("self_link")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(db.Self, db.Id)
}
```

#### QueryDatabases

```go
func main() {
	// ...
	dbs, err := client.QueryDatabases("SELECT * FROM ROOT r")
	if err != nil {
		log.Fatal(err)
	}
	for _, db := range dbs {
		fmt.Println("DB Name:", db.Id)
	}
}
```

#### ReadDatabases

```go
func main() {
	// ...
	dbs, err := client.ReadDatabases()
	if err != nil {
		log.Fatal(err)
	}
	for _, db := range dbs {
		fmt.Println("DB Name:", db.Id)
	}
}
```

#### CreateDatabase

```go
func main() {
	// ...
	db, err := client.CreateDatabase(`{ "id": "test" }`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(db)

	// or ...
	var db documentdb.Database
	db.Id = "test"
	db, err = client.CreateDatabase(&db)
}
```

#### ReplaceDatabase

```go
func main() {
	// ...
	db, err := client.ReplaceDatabase("self_link", `{ "id": "test" }`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(db)

	// or ...
	var db documentdb.Database
	db, err = client.ReplaceDatabase("self_link", &db)
}
```

#### DeleteDatabase

```go
func main() {
	// ...
	err := client.DeleteDatabase("self_link")
	if err != nil {
		log.Fatal(err)
	}
}
```

### Collections

#### ReadCollection

```go
func main() {
	// ...
	coll, err := client.ReadCollection("self_link")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(coll.Self, coll.Id)
}
```

#### QueryCollections

```go
func main() {
	// ...
	colls, err := client.QueryCollections("db_self_link", "SELECT * FROM ROOT r")
	if err != nil {
		log.Fatal(err)
	}
	for _, coll := range colls {
		fmt.Println("Collection Name:", coll.Id)
	}
}
```

#### ReadCollections

```go
func main() {
	// ...
	colls, err := client.ReadCollections("db_self_link")
	if err != nil {
		log.Fatal(err)
	}
	for _, coll := range colls {
		fmt.Println("Collection Name:", coll.Id)
	}
}
```

#### CreateCollection

```go
func main() {
	// ...
	coll, err := client.CreateCollection("db_self_link", `{"id": "my_test"}`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Collection Name:", coll.Id)

	// or ...
	var coll documentdb.Collection
	coll.Id = "test"
	coll, err = client.CreateCollection("db_self_link", &coll)
}
```

#### DeleteCollection

```go
func main() {
	// ...
	err := client.DeleteCollection("self_link")
	if err != nil {
		log.Fatal(err)
	}
}
```

### Documents

#### ReadDocument

```go
type Document struct {
	documentdb.Document
	// Your external fields
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
}

func main() {
	// ...
	var doc Document
	err = client.ReadDocument("self_link", &doc)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Document Name:", doc.Name)
}
```

#### QueryDocuments

```go
type User struct {
	documentdb.Document
	// Your external fields
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
}

func main() {
	// ...
	var users []User
	err = client.QueryDocuments("coll_self_link", "SELECT * FROM ROOT r", &users)
	if err != nil {
		log.Fatal(err)
	}
	for _, user := range users {
		fmt.Print("Name:", user.Name, "Email:", user.Email)
	}
}
```

#### QueryDocuments with partition key

```go
type User struct {
	documentdb.Document
	// Your external fields
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
}

func main() {
	// ...
	var users []User

	partitionKey := func(reqOpts *documentdb.RequestOptions) {
		reqOpts.PartitionKey = []string{"Your-Partition-Key"}
	}

	err = client.QueryDocumentsWithRequestOptions("coll_self_link", "SELECT * FROM ROOT r", &users, partitionKey)
	if err != nil {
		log.Fatal(err)
	}
	for _, user := range users {
		fmt.Print("Name:", user.Name, "Email:", user.Email)
	}
}
```

#### ReadDocuments

```go
type User struct {
	documentdb.Document
	// Your external fields
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
}

func main() {
	// ...
	var users []User
	err = client.ReadDocuments("coll_self_link", &users)
	if err != nil {
		log.Fatal(err)
	}
	for _, user := range users {
		fmt.Print("Name:", user.Name, "Email:", user.Email)
	}
}
```

#### CreateDocument

```go
type User struct {
	documentdb.Document
	// Your external fields
	Name    string `json:"name,omitempty"`
	Email   string `json:"email,omitempty"`
}

func main() {
	// ...
	var user User
	// Note: If the `id` is missing(or empty) in the payload it will generate
	// random document id(i.e: uuid4)
	user.Id = "uuid"
	user.Name = "Ariel"
	user.Email = "ariel@test.com"
	err := client.CreateDocument("coll_self_link", &doc)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print("Name:", user.Name, "Email:", user.Email)
}
```

#### ReplaceDocument

```go
type User struct {
	documentdb.Document
	// Your external fields
	IsAdmin bool   `json:"isAdmin,omitempty"`
}

func main() {
	// ...
	var user User
	user.Id = "uuid"
	user.IsAdmin = false
	err := client.ReplaceDocument("doc_self_link", &user)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Print("Is Admin:", user.IsAdmin)
}
```

#### DeleteDocument

```go
func main() {
	// ...
	err := client.DeleteDocument("doc_self_link")
	if err != nil {
		log.Fatal(err)
	}
}
```

###

#### ExecuteStoredProcedure

```go
func main() {
	// ...
	var docs []Document
	err := client.ExecuteStoredProcedure("sporc_self", [...]interface{}{p1, p2}, &docs)
	if err != nil {
		log.Fatal(err)
	}
	// ...
}
```

### Examples

* [Go DocumentDB Example](https://github.com/a8m/go-documentdb-example) - A users CRUD application using Martini and DocumentDB

### License

MIT

[travis-image]: https://img.shields.io/travis/a8m/documentdb-go.svg?style=flat-square
[travis-url]: https://travis-ci.org/a8m/documentdb-go
