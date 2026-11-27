package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/key"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

// pullJSON is --json on registry pull.
var pullJSON bool

// pullExt is what every pull lands as: an overlay is SquashFS, so the
// destination never varies by what was published.
const pullExt = ".sqf"

func newRegistryPullCommand(opts *registryOptions) *cobra.Command {
	pull := &cobra.Command{
		Use:   "pull [flags] <name|name@identity>",
		Short: "Install a published overlay",
		Long: `Install a published overlay, using the copy for this machine's architecture.

- A name installs the current build of that name, such as star/2.7.11b.
- name@identity installs one particular build.
- A snapshot must be pulled with --prefix.`,
		Example: `  condatainer registry pull star/2.7.11b --registry ghcr.io/my-lab/cnt
  condatainer registry pull star/2.7.11b@9f2c1ab --source lab
  condatainer registry pull star/2.7.11b --source lab -p ./overlays/star.sqf`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRegistryPull(cmd, opts, firstArg(args))
		},
	}
	jsonFlag(pull.Flags(), &pullJSON)
	addEndpointFlags(pull, opts)
	pull.Flags().StringVar(&opts.digest, "digest", "", "Install the copy with this digest (sha256:<hex>)")
	pull.Flags().StringVarP(&opts.prefix, "prefix", "p", "", "Install at this path (the image extension is optional)")
	return pull
}

// requirePrefixForFrozen refuses a frozen environment pulled without --prefix.
// Its project addresses it by path, so it is never filed under a name.
func requirePrefixForFrozen(annotations map[string]string, prefix string) error {
	if prefix == "" && annotations[registry.AnnIdentityScheme] == string(key.PayloadTreeV1) {
		return fmt.Errorf("a snapshot must be pulled with --prefix")
	}
	return nil
}

func runRegistryPull(cmd *cobra.Command, opts *registryOptions, spec string) error {
	resolved, ep, err := resolveRegistryArtifact(cmd, opts, spec)
	if err != nil {
		return err
	}
	if err := requirePrefixForFrozen(resolved.annotations, opts.prefix); err != nil {
		return err
	}
	dest, err := registryPullDestination(opts.prefix, resolved.name, resolved.annotations[registry.AnnTitle])
	if err != nil {
		return err
	}
	pullErr := registry.Pull(ep.ctx, resolved.base, resolved.repo, resolved.desc, resolved.annotations, dest)
	if pullJSON {
		return writePullStep(cmd, resolved, ep.repository, dest, pullErr)
	}
	if pullErr != nil {
		return pullErr
	}
	reportDone(cmd, "pulled", dest)
	return nil
}

// writePullStep prints the one fetch step of a pull as the steps document. A
// pull that failed prints it with outcome failed, and ends with the error.
func writePullStep(cmd *cobra.Command, found resolvedRegistryArtifact, repository, dest string, pullErr error) error {
	ann := found.annotations
	step := report.Step{
		Overlay:  report.Overlay{Name: ann[registry.AnnTitle], Arch: ann[registry.AnnArch]},
		Action:   report.ActionFetch,
		Endpoint: repository,
		Digest:   found.desc.Digest.String(),
		Identity: registry.Identity(ann),
		Equiv:    registry.Equiv(ann),
		Outcome:  report.OutcomeDone,
		Location: plannedLocation(dest, pullErr == nil),
	}
	if pullErr != nil {
		step.Outcome, step.Error = report.OutcomeFailed, pullErr.Error()
	}
	doc := &report.Steps{Steps: report.List[report.Step]{step}}
	if err := report.Write(cmd.OutOrStdout(), doc); err != nil {
		return err
	}
	return pullErr
}

// registryPullDestination picks where a pull installs. --prefix names the path
// and gains the extension when it has none; otherwise the artifact's name, or the
// one it was published as when only a digest was given, goes into the writable
// images directory.
func registryPullDestination(prefix, name, title string) (string, error) {
	if prefix != "" {
		base := filepath.Base(prefix)
		if !utils.IsSqf(base) {
			return prefix + pullExt, nil
		}
		if strings.TrimSuffix(base, filepath.Ext(base)) == "" {
			return "", fmt.Errorf("--prefix %s has no file name before %s", prefix, filepath.Ext(base))
		}
		return prefix, nil
	}

	if name == "" {
		name = utils.NormalizeModule(title)
	}
	if name == "" {
		return "", fmt.Errorf("cannot choose an install name from this address; use --prefix")
	}
	dir, err := config.GetWritableImagesDir()
	if err != nil {
		return "", fmt.Errorf("no writable images directory: %w", err)
	}
	return filepath.Join(dir, strings.ReplaceAll(name, "/", "--")+pullExt), nil
}
