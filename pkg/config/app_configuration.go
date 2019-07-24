package config

// ApplicationConfig is an optional config supplied by user code.
type ApplicationConfig struct {
	Entities []string `json:"entities"`
	// Duration. example: "1h"
	ActorIdleTimeout string `json:"actorIdleTimeout"`
	// Duration. example: "30s"
	ActorScanInterval string `json:"actorScanInterval"`
}
