package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/spf13/cobra"
)

func newRegistryResolveCommand(opts *registryOptions) *cobra.Command {
	var asJSON bool
	resolve := &cobra.Command{
		Use:   "resolve [flags] <name|name@identity>",
		Short: "Print the digest of a published overlay without downloading it",
		Example: `  condatainer registry resolve star/2.7.11b --registry ghcr.io/my-lab/cnt
  condatainer registry resolve star/2.7.11b --source lab --arch arm64`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, _, err := resolveRegistryArtifact(cmd, opts, firstArg(args))
			if err != nil {
				return err
			}
			if asJSON {
				spec := firstArg(args)
				if opts.digest != "" {
					spec = ""
				}
				return report.Write(cmd.OutOrStdout(), resolution(resolved, spec, opts.arch))
			}
			fmt.Fprintln(cmd.OutOrStdout(), resolved.desc.Digest)
			return nil
		},
	}
	jsonFlag(resolve.Flags(), &asJSON)
	addEndpointFlags(resolve, opts)
	resolve.Flags().StringVar(&opts.digest, "digest", "", "Check the copy with this digest (sha256:<hex>)")
	resolve.Flags().StringVar(&opts.arch, "arch", "", "Machine architecture: amd64, arm64 or noarch")
	return resolve
}

// resolution is the resolution document for a found copy. The tag is the one
// the address asked for, from the copy's own keys, and absent for a digest.
func resolution(found resolvedRegistryArtifact, spec, arch string) *report.Resolution {
	ann := found.annotations
	out := report.Resolved{
		Overlay:  report.Overlay{Name: ann[registry.AnnTitle], Arch: ann[registry.AnnArch]},
		Digest:   found.desc.Digest.String(),
		Identity: registry.Identity(ann),
		Equiv:    registry.Equiv(ann),
	}
	if addr, err := parseRegistryAddress(spec, arch); err == nil && spec != "" {
		var tag string
		switch {
		case addr.Identity != "":
			tag, _ = registry.BuildTag(out.Name, out.Identity, out.Arch)
		case addr.Equiv != "":
			tag, _ = registry.EquivTag(out.Name, out.Equiv, out.Arch)
		default:
			tag, _ = registry.NameTag(out.Name, out.Arch)
		}
		out.Tag = tag
	}
	return &report.Resolution{Overlay: out}
}
