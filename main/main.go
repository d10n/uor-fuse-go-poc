package main

import (
	"os"
	"path/filepath"

	"github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/uor-framework/uor-fuse-go/cli"
	"github.com/uor-framework/uor-fuse-go/cli/log"
	"github.com/uor-framework/uor-fuse-go/config"
)

func main() {
	rootCmd := NewRootCmd()
	cobra.CheckErr(rootCmd.Execute())
}

// NewRootCmd creates a new cobra.Command for the command root.
func NewRootCmd() *cobra.Command {
	o := config.RootOptions{}

	o.IOStreams = genericclioptions.IOStreams{
		In:     os.Stdin,
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}
	o.EnvConfig = config.ReadEnvConfig()
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

	cmd.AddCommand(cli.NewMountCmd(&o))
	cmd.AddCommand(cli.NewVersionCmd(&o))

	return cmd
}
