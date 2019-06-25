package controller

import "github.com/kelseyhightower/envconfig"

type Config struct {
	ActionsRuntimeImage string `envconfig:"RUNTIME_IMAGE" required:"true"`
}

func GetConfigFromEnvironment() (Config, error) {
	c := Config{}
	err := envconfig.Process("", &c)

	return c, err
}
