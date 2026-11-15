package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var configSourceAddCmd = &cobra.Command{
	Use:   "add [flags] <name> <url|directory>",
	Short: "Add or replace a recipe source",
	Long: `Add a recipe source, or replace the URL of one already in the layer.

- The source is read first. Nothing is saved unless its recipes can be read.
- A new source goes last in its layer unless a position flag says otherwise.

Tokens:
- If the source needs a token, a recipe token is asked for.
- If it also declares a registry, a registry token is asked for. Empty skips it.
- A token for a layer other than user is asked for only after you confirm it.`,
	Example: `  condatainer config source add lab /shared/lab/recipes
  condatainer config source add lab https://raw.githubusercontent.com/my-lab/recipes/main
  condatainer config source add lab https://raw.githubusercontent.com/my-lab/recipes/main -l extra-root --first
  # Tokens from stdin, saved in a shared layer: -y confirms the layer
  printf '%s\n%s\n' "$RECIPE_TOKEN" "$REGISTRY_TOKEN" | condatainer config source add lab https://raw.githubusercontent.com/my-lab/recipes/main -l extra-root --token-stdin -y`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE:         runSourceAdd,
}

// tokenLines answers token prompts from stdin under --token-stdin, one line
// each, in the order they are asked. Set per run of add.
var tokenLines *bufio.Reader

func init() {
	placementFlags(configSourceAddCmd)
	configSourceAddCmd.Flags().BoolVar(&sourceOpts.tokenStdin, "token-stdin", false, "Read tokens from stdin, one per line: the recipe token, then the registry token")
	configSourceAddCmd.Flags().StringVarP(&setLayer, "layer", "l", "", "Layer to write: "+layerChoices)
}

func runSourceAdd(cmd *cobra.Command, args []string) error {
	tokenLines = nil
	name, base := args[0], strings.TrimRight(args[1], "/")
	if !isSourceURL(base) {
		abs, err := filepath.Abs(base)
		if err != nil {
			return err
		}
		base = abs
	}
	path, layer, err := writableSourceLayer()
	if err != nil {
		return err
	}
	spec := catalog.Spec{Name: name, Base: base}
	key := credential.TrimScheme(base)

	src, err := catalog.Probe(cmd.Context(), spec)
	token := ""
	if err != nil {
		if !isSourceURL(base) || !errors.Is(err, catalog.ErrUnreadable) {
			return err
		}
		if layer != "user" && !confirmSharedToken(cmd, name, layer) {
			return nil
		}
		var asked bool
		token, asked, err = readSecret(cmd, fmt.Sprintf("Recipe token for %s: ", name), sourceOpts.tokenStdin)
		if err != nil {
			return err
		}
		if !asked || token == "" {
			return fmt.Errorf("cannot read %s; if it is private, give its token with --token-stdin", base)
		}
		src, err = catalog.Probe(cmd.Context(), catalog.Spec{Name: name, Base: base, Token: catalog.NewToken(token, key, layer)})
		if err != nil {
			return fmt.Errorf("cannot read %s with this token; check the URL and the token's access: %w", name, err)
		}
	}

	list := config.LayerSourceList(path)
	placed, err := placeIn(list, spec, sourcePosition(), layer)
	if err != nil {
		return err
	}
	file, err := config.CredentialFile(layer)
	if err != nil {
		return err
	}
	for _, old := range list {
		if old.Name == name && old.Base != base {
			dropToken(file, credential.TrimScheme(old.Base))
		}
	}
	if token != "" {
		if err := credential.Save(file, credential.Source, key, credential.Credential{Username: credential.TokenUser, Secret: token}); err != nil {
			return err
		}
	} else {
		dropToken(file, key)
	}
	if err := config.WriteLayerSourceList(path, placed); err != nil {
		return err
	}
	utils.PrintMessage("Added source %s (%s layer)", name, utils.StyleName(layer))
	warnDescriptor(name, src.DescriptorErr)

	if token != "" {
		saveSourceRegistry(cmd, name, base, file, src.Desc.OCI.Registry)
		noteTokenFile(file)
	}
	return nil
}

// confirmSharedToken asks, before any token is entered, whether to save it in
// layer rather than the user's. -y answers yes; with --token-stdin and no -y the
// answer is no.
func confirmSharedToken(cmd *cobra.Command, name, layer string) bool {
	if utils.ShouldAnswerYes() {
		return true
	}
	if sourceOpts.tokenStdin {
		utils.PrintError("With --token-stdin, pass -y to save the token in the %s layer", utils.StyleName(layer))
		return false
	}
	return utils.Confirm(cmd.Context(), cmd.ErrOrStderr(), fmt.Sprintf("%s needs a token. Save it in the %s layer? [y/N]: ", name, utils.StyleName(layer)))
}

// noteTokenFile shows the mode of the credential file a token was just saved
// in, and warns when others can replace it.
func noteTokenFile(file credential.File) {
	if perm, group, ok := credential.Mode(file.Path); ok {
		utils.PrintMessage("Token saved in %s, mode %s, group %s.", file.Path, utils.StyleName(fmt.Sprintf("%04o", perm)), group)
	}
	printFindings(credential.Findings(file.Layer, file.Path))
}

// saveSourceRegistry asks for a token for a private source's registry and
// saves it with the source once the registry accepts it. A token the source
// already holds for that registry is kept without asking.
func saveSourceRegistry(cmd *cobra.Command, name, base string, file credential.File, endpoint string) {
	if endpoint == "" {
		return
	}
	if found, ok := credential.LookupSourceRegistry([]credential.File{file}, base); ok && found.Key == endpoint {
		return
	}
	utils.PrintMessage("Registry for %s: %s", name, endpoint)
	token, asked, err := readSecret(cmd, fmt.Sprintf("Registry token for %s, empty to skip: ", name), sourceOpts.tokenStdin)
	if err != nil || !asked || token == "" {
		say := utils.PrintMessage
		if sourceOpts.tokenStdin {
			say = utils.PrintNote // nobody saw a prompt to skip
		}
		say("No registry token saved for %s. To add it, run `condatainer config source add` again with both tokens.", name)
		return
	}
	cred := credential.Credential{Username: credential.TokenUser, Secret: token}
	if err := registry.CheckLogin(cmd.Context(), endpoint, cred.Username, cred.Secret); err != nil {
		utils.PrintWarning("%v. To retry, run `condatainer config source add` again.", err)
		return
	}
	if err := credential.SaveSourceRegistry(file, credential.TrimScheme(base), endpoint, cred); err != nil {
		utils.PrintWarning("Could not save the registry token: %v", err)
		return
	}
	utils.PrintMessage("Saved the registry token for %s", name)
}

// readSecret reads a token: the next line of stdin with fromStdin, else hidden
// from the terminal after prompt. asked is false when stdin has no line left or
// there is no terminal to ask.
func readSecret(cmd *cobra.Command, prompt string, fromStdin bool) (secret string, asked bool, err error) {
	secret, asked, err = readRawSecret(cmd, prompt, fromStdin)
	if err != nil || !asked {
		return "", asked, err
	}
	secret, err = cleanSecret(secret)
	return secret, true, err
}

// cleanSecret drops the markers a terminal wraps a paste in, and refuses a token
// left with a space or control character, which no HTTP header can carry.
func cleanSecret(raw string) (string, error) {
	s := strings.NewReplacer("\x1b[200~", "", "\x1b[201~", "").Replace(raw)
	s = strings.TrimSpace(s)
	for _, r := range s {
		if r <= ' ' || r == 0x7f {
			return "", errors.New("the token contains a space or control character; enter it again")
		}
	}
	return s, nil
}

// readRawSecret is readSecret without the cleaning.
func readRawSecret(cmd *cobra.Command, prompt string, fromStdin bool) (secret string, asked bool, err error) {
	if fromStdin {
		if tokenLines == nil {
			tokenLines = bufio.NewReader(cmd.InOrStdin())
		}
		line, err := tokenLines.ReadString('\n')
		if errors.Is(err, io.EOF) {
			return strings.TrimSpace(line), line != "", nil
		}
		if err != nil {
			return "", false, fmt.Errorf("cannot read the token from stdin: %w", err)
		}
		return strings.TrimSpace(line), true, nil
	}
	file, ok := cmd.InOrStdin().(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return "", false, nil
	}
	fmt.Fprint(cmd.ErrOrStderr(), prompt)
	data, err := term.ReadPassword(int(file.Fd()))
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", false, fmt.Errorf("cannot read the token: %w", err)
	}
	return strings.TrimSpace(string(data)), true, nil
}
