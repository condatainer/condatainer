package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newRegistryLoginCommand(opts *registryOptions) *cobra.Command {
	login := &cobra.Command{
		Use:   "login [flags] <host>[/<owner>/<repo>]",
		Short: "Verify and store registry credentials",
		Long: `Check credentials against the registry, and save them once they work.

- A repository's credential is used before its host's.
- Without --username or --password, they are asked for on the terminal.

` + configLayersHelp,
		Example: `  condatainer registry login ghcr.io -u my-user
  condatainer registry login ghcr.io/my-lab/rnaseq -u my-user --password-stdin
  condatainer registry login ghcr.io -l extra-root -u my-user`,
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			layer := config.NormalizeConfigLayer(opts.layer)
			if layer != "user" {
				if opts.passwordStdin && !utils.ShouldAnswerYes() {
					return fmt.Errorf("saving to the %s layer needs confirmation; pass -y to confirm when the password comes from stdin", layer)
				}
				if !confirmSharedLogin(cmd, layer) {
					return nil
				}
			}
			username := registryUsername(cmd, opts)
			password, err := registryPassword(cmd, opts)
			if err != nil {
				return err
			}
			if err := registry.Login(cmd.Context(), args[0], layer, username, password); err != nil {
				return err
			}
			reportDone(cmd, "logged in to", registry.TrimBaseScheme(args[0])+" ("+layer+" layer)")
			reportPermissions(layer)
			return nil
		},
	}
	login.Flags().StringVarP(&opts.username, "username", "u", "", "Registry username")
	login.Flags().StringVarP(&opts.password, "password", "p", "", "Registry password or token")
	login.Flags().BoolVar(&opts.passwordStdin, "password-stdin", false, "Read the password/token from stdin")
	login.Flags().StringVarP(&opts.layer, "layer", "l", "user", "Layer to save in: "+layerChoices)
	return login
}

// registryUsername is --username, or one typed on the terminal when it is
// omitted. Anything else is left empty, and the registry refuses it.
func registryUsername(cmd *cobra.Command, opts *registryOptions) string {
	if opts.username != "" {
		return opts.username
	}
	if file, ok := cmd.InOrStdin().(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		fmt.Fprint(cmd.ErrOrStderr(), "Username: ")
		name, _ := utils.ReadLineContext(cmd.Context())
		return name
	}
	return ""
}

func registryPassword(cmd *cobra.Command, opts *registryOptions) (string, error) {
	if opts.passwordStdin && opts.password != "" {
		return "", fmt.Errorf("--password and --password-stdin are mutually exclusive")
	}
	if opts.passwordStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("cannot read password from stdin: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if opts.password != "" {
		return opts.password, nil
	}
	file, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", fmt.Errorf("no password provided; use --password or --password-stdin")
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Password/Token: ")
	password, err := term.ReadPassword(int(file.Fd()))
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", fmt.Errorf("cannot read password: %w", err)
	}
	return string(password), nil
}

// confirmSharedLogin warns that a credential saved outside the user's own layer
// can be read by anyone who can read that file, and asks to go on. -y answers yes.
func confirmSharedLogin(cmd *cobra.Command, layer string) bool {
	utils.PrintWarning("The credential is saved in the %s layer: anyone who can read that file can use it. Use a read-only token.", layer)
	utils.PrintNote("The file is written 0600, or 0660 when its directory is group-writable.")
	if utils.ShouldAnswerYes() {
		return true
	}
	return utils.Confirm(cmd.Context(), cmd.ErrOrStderr(), "Continue? [y/N]: ")
}

// reportPermissions tells the person who just saved a credential if the file
// can be reached by more people than it should.
func reportPermissions(layer string) {
	if file, err := config.CredentialFile(layer); err == nil {
		printFindings(credential.Findings(layer, file.Path))
	}
}
