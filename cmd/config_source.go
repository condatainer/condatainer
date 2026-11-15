package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

var sourceOpts struct {
	first, last   bool
	before, after string
	tokenStdin    bool
}

const sourceOrderHelp = `Search order (first match wins):
- Layers first: user > extra-root > cnt-root.
- Within a layer, the order of its list.
- cnt last, unless a layer lists it.`

var configSourceCmd = &cobra.Command{
	Use:   "source",
	Short: "Manage recipe sources",
	Long: `Add, order and remove the recipe sources searched for recipes.

A source is a URL serving a recipe collection, or a directory holding one.
A private URL needs a token, asked for when the source is added.

` + sourceOrderHelp + `

` + configLayersHelp,
}

func init() {
	configSourceCmd.AddCommand(configSourceAddCmd, configSourceMoveCmd, configSourceListCmd, configSourceRemoveCmd)
}

// placementFlags adds the flags that choose where a source goes in its layer.
func placementFlags(c *cobra.Command) {
	c.Flags().BoolVar(&sourceOpts.first, "first", false, "Put it first in its layer")
	c.Flags().BoolVar(&sourceOpts.last, "last", false, "Put it last in its layer")
	c.Flags().StringVar(&sourceOpts.before, "before", "", "Put it just before this source")
	c.Flags().StringVar(&sourceOpts.after, "after", "", "Put it just after this source")
	c.MarkFlagsMutuallyExclusive("first", "last", "before", "after")
	_ = c.RegisterFlagCompletionFunc("before", sourceNamesCompletion)
	_ = c.RegisterFlagCompletionFunc("after", sourceNamesCompletion)
}

// sourceNameCompletion completes the one source name a command takes.
func sourceNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return sourceNamesCompletion(cmd, args, toComplete)
}

// sourceNamesCompletion offers the configured source names, in search order.
func sourceNamesCompletion(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	var names []string
	for _, s := range config.ResolvedSources() {
		names = append(names, s.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func sourcePosition() config.Position {
	return config.Position{
		First: sourceOpts.first, Last: sourceOpts.last,
		Before: sourceOpts.before, After: sourceOpts.after,
	}
}

func isSourceURL(base string) bool {
	return strings.HasPrefix(base, "https://") || strings.HasPrefix(base, "http://")
}

// writableSourceLayer is the config file and layer a source command writes,
// from -l or the active config, warning when a read-only one fell back to the user's.
func writableSourceLayer() (path, layer string, err error) {
	path, layer, fellBackFrom, err := config.ResolveWritableConfigPathVerbose(setLayer)
	if err != nil {
		return "", "", err
	}
	if fellBackFrom != "" {
		utils.PrintWarning("%s config is read-only; saving to your user config instead (applies only to you).", fellBackFrom)
	}
	return path, layer, nil
}

// holdingLayer is the layer and config file a source command acts on: -l, or the
// nearest layer that lists name. The default cnt is held by the writable layer.
func holdingLayer(name string) (path, layer string, err error) {
	if setLayer != "" {
		return writableSourceLayer()
	}
	for _, s := range config.ResolvedSources() {
		if s.Name != name {
			continue
		}
		if s.Layer == config.DefaultLayer {
			return writableSourceLayer()
		}
		path, layer, err := config.ResolveWritableConfigPath(s.Layer)
		return path, layer, err
	}
	return "", "", fmt.Errorf("no source named %s; see `condatainer config source list`", name)
}

// placeIn applies pos to list, explaining a reference to a source in another layer.
func placeIn(list []catalog.Spec, spec catalog.Spec, pos config.Position, layer string) ([]catalog.Spec, error) {
	out, err := config.PlaceSource(list, spec, pos)
	if errors.Is(err, config.ErrSourceNotFound) {
		other := pos.Before + pos.After
		for _, s := range config.ResolvedSources() {
			if s.Name == other {
				return nil, fmt.Errorf("%s is in the %s layer, not %s; a source is placed only within its layer", other, utils.StyleName(s.Layer), utils.StyleName(layer))
			}
		}
		return nil, fmt.Errorf("no source named %s in the %s layer", other, utils.StyleName(layer))
	}
	return out, err
}

// warnDescriptor says that a source's source.json is unusable, which otherwise shows only when a registry command needs it.
func warnDescriptor(name string, err error) {
	if err != nil {
		utils.PrintWarning("Source %s: source.json is ignored, so its registry and provenance are unavailable: %v", name, err)
	}
}

// dropToken removes a source's tokens from a layer, if it holds them.
func dropToken(file credential.File, key string) {
	if err := credential.Remove(file, credential.Source, key); err != nil && !errors.Is(err, credential.ErrNotStored) {
		utils.PrintWarning("Could not remove the token for %s: %v", key, err)
	}
}

// printSourcesKey prints `config get sources`: the search order, or one layer's
// list with -l.
func printSourcesKey() {
	if getLayer != "" {
		path, _, err := config.ResolveReadableConfigPath(getLayer)
		if err != nil {
			ExitWithError("Invalid layer: %v", err)
		}
		for _, s := range config.LayerSourceList(path) {
			fmt.Printf("%s: %s\n", s.Name, s.Base)
		}
		return
	}
	for _, s := range config.ResolvedSources() {
		fmt.Printf("%s: %s\n", s.Name, s.Base)
	}
}
