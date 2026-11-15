package cmd

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/spf13/cobra"
)

var configSourceListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List recipe sources in search order",
	Long: `List recipe sources in search order, with the layer each comes from.

- A source is private when it is read with a token, or is a directory.
- Under each source, its registry and the credential prebuilt pulls send first.
- Tokens are never shown.

` + sourceOrderHelp,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runSourceList,
}

func runSourceList(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	cat, _ := config.OpenCatalog(cmd.Context())
	descs := map[string]catalog.Descriptor{}
	for _, s := range cat {
		descs[s.Name] = s.Desc
	}
	files := config.CredentialFiles()
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	defer func() {
		for _, s := range cat {
			warnDescriptor(s.Name, s.DescriptorErr)
		}
	}()
	fmt.Fprintln(tw, "#\tNAME\tURL\tLAYER\tACCESS")
	for i, s := range config.ResolvedSources() {
		access := "public"
		if !isSourceURL(s.Base) {
			access = "private"
		} else if found, ok := credential.Lookup(files, credential.Source, s.Base); ok {
			access = "private, token: " + credentialHolder(found)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", i+1, s.Name, s.Base, s.Layer, access)
		if reg := descs[s.Name].OCI.Registry; reg != "" {
			fmt.Fprintf(tw, "\t\tregistry %s\t\t%s\n", reg, registryHolder(cmd.Context(), s.Base, reg))
		}
	}
	return tw.Flush()
}

// credentialHolder names whose credential a lookup found: "you" for the user
// layer, else the layer, or GITHUB_TOKEN.
func credentialHolder(found credential.Found) string {
	switch found.Layer {
	case "user":
		return "you"
	case registry.EnvLayer:
		return registry.EnvGitHubToken
	}
	return found.Layer
}

// registryHolder names the credential a source's prebuilt pulls send first.
func registryHolder(ctx context.Context, base, endpoint string) string {
	found, ok := registry.CredentialFor(registry.WithSource(ctx, base), endpoint)
	switch {
	case !ok:
		return "anonymous"
	case found.Source != "":
		return "source token (" + credentialHolder(found) + ")"
	case found.Layer == registry.EnvLayer:
		return registry.EnvGitHubToken
	}
	return "login (" + credentialHolder(found) + ")"
}
