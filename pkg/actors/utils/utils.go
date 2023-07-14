package utils

const (
	DaprSeparator        = "||"
	MetadataPartitionKey = "partitionKey"
	MetadataZeroID       = "00000000-0000-0000-0000-000000000000"

	ErrStateStoreNotFound      = "actors: state store does not exist or incorrectly configured"
	ErrStateStoreNotConfigured = `actors: state store does not exist or incorrectly configured. Have you set the property '{"name": "actorStateStore", "value": "true"}' in your state store component file?`
)

// func IsActorLocal(targetActorAddress, hostAddress string, grpcPort int) bool {
// 	return strings.Contains(targetActorAddress, "localhost") || strings.Contains(targetActorAddress, "127.0.0.1") ||
// 		targetActorAddress == hostAddress+":"+strconv.Itoa(grpcPort)
// }
