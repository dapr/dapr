package documentdb

// Resource
type Resource struct {
	Id	string	`json:"id,omitempty"`
	Self	string	`json:"_self,omitempty"`
	Etag	string	`json:"_etag,omitempty"`
	Rid	string	`json:"_rid,omitempty"`
	Ts	int	`json:"_ts,omitempty"`
}

// Indexing policy
// TODO: Ex/IncludePaths
type IndexingPolicy struct {
	IndexingMode	string	`json: "indexingMode,omitempty"`
	Automatic	bool	`json: "automatic,omitempty"`
}

// Database
type Database struct {
	Resource
	Colls	string	`json:"_colls,omitempty"`
	Users	string	`json:"_users,omitempty"`
}

// Collection
type Collection struct {
	Resource
	IndexingPolicy	IndexingPolicy	`json:"indexingPolicy,omitempty"`
	Docs		string		`json:"_docs,omitempty"`
	Udf		string		`json:"_udfs,omitempty"`
	Sporcs		string		`json:"_sporcs,omitempty"`
	Triggers	string		`json:"_triggers,omitempty"`
	Conflicts	string		`json:"_conflicts,omitempty"`
}

// Document
type Document struct {
	Resource
	attachments	string	`json: "attachments,omitempty"`
}

// Stored Procedure
type Sproc struct {
	Resource
	Body	string	`json:"body,omitempty"`
}

// User Defined Function
type UDF struct {
	Resource
	Body	string	`json:"body,omitempty"`
}
