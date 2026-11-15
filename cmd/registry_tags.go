package cmd

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/spf13/cobra"
)

func newRegistryTagsCommand(opts *registryOptions) *cobra.Command {
	var asJSON bool
	tags := &cobra.Command{
		Use:   "tags [flags]",
		Short: "List what a registry holds",
		Long: `Print every tag in the registry. Each ends with its architecture.

- name: star--2.7.11b__amd64
- identity: star--2.7.11b__9f2c1ab4d3e8__amd64 (one exact build)
- equivalence: star--2.7.11b__eq-e90d3b7714cf__amd64 (newest of one recipe)`,
		Example: `  condatainer registry tags --source lab
  condatainer registry tags --registry ghcr.io/my-lab/cnt | grep star`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ep, err := resolveRegistryEndpoint(cmd, opts)
			if err != nil {
				return err
			}
			base, repo, err := registry.SplitCoordinate(ep.repository)
			if err != nil {
				return err
			}
			listed, err := registry.ListTags(ep.ctx, base, repo)
			if err != nil {
				return err
			}
			if asJSON {
				return report.Write(cmd.OutOrStdout(), tagsDoc(ep.repository, listed))
			}
			for _, tag := range listed {
				fmt.Fprintln(cmd.OutOrStdout(), tag)
			}
			return nil
		},
	}
	jsonFlag(tags.Flags(), &asJSON)
	addEndpointFlags(tags, opts)
	return tags
}

// firstArg is the one optional argument, or empty.
// tagsDoc is the tags document for a repository's tags.
func tagsDoc(repository string, listed []string) *report.Tags {
	doc := &report.Tags{Repository: repository, Tags: make(report.List[report.Tag], 0, len(listed))}
	for _, tag := range listed {
		info := registry.DescribeTag(tag)
		doc.Tags = append(doc.Tags, report.Tag{Tag: tag, Name: info.Name, Kind: info.Kind, Key: info.Key, Arch: info.Arch})
	}
	return doc
}
