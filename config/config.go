package config

import (
	"os"
	"strconv"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/uor-framework/uor-fuse-go/cli/log"
)

// EnvConfig stores CLI runtime configuration from environment variables.
// Struct field names should match the name of the environment variable that the field is derived from.
type EnvConfig struct {
	UOR_DEV_MODE bool // true: show unimplemented stubs in --help
}

func ReadEnvConfig() EnvConfig {
	envConfig := EnvConfig{}

	devModeString := os.Getenv("UOR_DEV_MODE")
	devMode, err := strconv.ParseBool(devModeString)
	envConfig.UOR_DEV_MODE = err == nil && devMode

	return envConfig
}

// RootOptions describe global configuration options that can be set.
type RootOptions struct {
	IOStreams genericclioptions.IOStreams
	LogLevel  string
	Logger    log.Logger
	CacheDir  string
	EnvConfig
}
