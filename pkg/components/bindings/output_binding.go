package bindings

type OutputBinding interface {
	Init(metadata Metadata) error
	Write(req *WriteRequest) error
}
