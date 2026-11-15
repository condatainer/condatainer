package cmd

import (
	"errors"
	"fmt"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newRegistryLogoutCommand(opts *registryOptions) *cobra.Command {
	logout := &cobra.Command{
		Use:          "logout [flags] <host>[/<owner>/<repo>]",
		Short:        "Remove stored registry credentials",
		Long:         `Remove the credential saved under exactly this host or repository.`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			layer := config.NormalizeConfigLayer(opts.layer)
			if err := registry.Logout(cmd.Context(), args[0], layer); err != nil {
				if name := sourceHolding(registry.TrimBaseScheme(args[0]), layer); errors.Is(err, credential.ErrNotStored) && name != "" {
					return fmt.Errorf("%s in the %s layer is the registry token of source %s; remove it with `condatainer config source remove %s`",
						registry.TrimBaseScheme(args[0]), utils.StyleName(layer), name, name)
				}
				return err
			}
			reportDone(cmd, "logged out of", registry.TrimBaseScheme(args[0])+" ("+layer+" layer)")
			return nil
		},
	}
	logout.Flags().StringVarP(&opts.layer, "layer", "l", "user", "Layer to remove from: "+layerChoices)
	return logout
}

// sourceHolding names the source whose registry token is stored under key in
// layer, or "".
func sourceHolding(key, layer string) string {
	stored, err := registryCredentials()
	if err != nil {
		return ""
	}
	for _, c := range stored {
		if c.Source != "" && c.Key == key && c.Layer == layer {
			return c.Source
		}
	}
	return ""
}
