package controller

import "github.com/kelseyhightower/envconfig"

type Config struct {
	ActionsRuntimeImage string `envconfig:"RUNTIME_IMAGE" required:"true"`
	ImagePullSecretName string `envconfig:"IMAGE_PULL_SECRET_NAME" required:"false"`
	Namespace           string `envconfig:"NAMESPACE" required:"true"`
}

func GetConfigFromEnvironment() (Config, error) {
	c := Config{}
	err := envconfig.Process("", &c)

	return c, err
}
