package cli

import (
	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/uor-framework/uor-fuse-go/cli/log"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"os"
	"path/filepath"
	"strconv"
)

// EnvConfig stores CLI runtime configuration from environment variables.
// Struct field names should match the name of the environment variable that the field is derived from.
type EnvConfig struct {
	UOR_DEV_MODE bool // true: show unimplemented stubs in --help
}

// RootOptions describe global configuration options that can be set.
type RootOptions struct {
	IOStreams genericclioptions.IOStreams
	LogLevel  string
	Logger    log.Logger
	CacheDir  string
	EnvConfig
}

func readEnvConfig() EnvConfig {
	envConfig := EnvConfig{}

	devModeString := os.Getenv("UOR_DEV_MODE")
	devMode, err := strconv.ParseBool(devModeString)
	envConfig.UOR_DEV_MODE = err == nil && devMode

	return envConfig
}

// NewRootCmd creates a new cobra.Command for the command root.
func NewRootCmd() *cobra.Command {
	o := RootOptions{}

	o.IOStreams = genericclioptions.IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
	o.EnvConfig = readEnvConfig()
	cmd := &cobra.Command{
		Use:   filepath.Base(os.Args[0]),
		Short: "UOR FUSE Driver",
		//Long:          clientLong,
		SilenceErrors: false,
		SilenceUsage:  false,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			logger, err := log.NewLogger(o.IOStreams.Out, o.LogLevel)
			if err != nil {
				return err
			}
			o.Logger = logger

			cacheEnv := os.Getenv("UOR_CACHE")
			if cacheEnv != "" {
				o.CacheDir = cacheEnv
			} else {
				home, err := homedir.Dir()
				if err != nil {
					return err
				}
				o.CacheDir = filepath.Join(home, ".uor", "cache")
			}

			return os.MkdirAll(o.CacheDir, 0750)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	f := cmd.PersistentFlags()
	f.StringVarP(&o.LogLevel, "loglevel", "l", "info",
		"Log level (debug, info, warn, error, fatal)")

	cmd.AddCommand(NewMountCmd(&o))
	cmd.AddCommand(NewVersionCmd(&o))

	return cmd
}
