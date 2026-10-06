package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/condatainer/condatainer/internal/catalog"
	"github.com/condatainer/condatainer/internal/config"
	"github.com/condatainer/condatainer/internal/credential"
	"github.com/condatainer/condatainer/internal/registry"
	"github.com/condatainer/condatainer/internal/utils"
)

var sourceOpts struct {
	first, last   bool
	before, after string
	tokenStdin    bool
}

// tokenLines answers token prompts from stdin under --token-stdin, one line
// each, in the order they are asked. Set per run of add.
var tokenLines *bufio.Reader

const sourceOrderHelp = `Search order (first match wins):
- Layers first: user, extra-root, app-root.
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

var configSourceAddCmd = &cobra.Command{
	Use:   "add [flags] <name> <url|directory>",
	Short: "Add or replace a recipe source",
	Long: `Add a recipe source, or replace the URL of one already in the layer.

- The source is read first. Nothing is saved unless its recipes can be read.
- If it needs a token, a recipe token is asked for.
- If it needed a token and declares a registry, a registry token is asked for too. Leave it empty to skip.
- With --token-stdin, each line of stdin answers one of these, in that order.
- A token for a layer other than user is asked for only after you confirm the layer. With --token-stdin, -y confirms it.
- Tokens are saved in the layer's credentials.json, never in config.yaml.
- A new source goes last in its layer unless a position flag says otherwise.

` + sourceOrderHelp,
	Example: `  condatainer config source add lab /shared/lab/recipes
  condatainer config source add lab https://raw.githubusercontent.com/my-lab/recipes/main
  condatainer config source add lab https://raw.githubusercontent.com/my-lab/recipes/main -l extra-root --first
  printf '%s\n%s\n' "$RECIPE_TOKEN" "$REGISTRY_TOKEN" | condatainer config source add lab https://raw.githubusercontent.com/my-lab/recipes/main --token-stdin`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE:         runSourceAdd,
}

var configSourceMoveCmd = &cobra.Command{
	Use:   "move [flags] <name>",
	Short: "Move a recipe source within its layer",
	Long: `Move a recipe source within its config layer. Its URL and token are kept.

- Give exactly one of --first, --last, --before, --after.
- The other source must be in the same layer.
- Moving the default cnt writes it into the layer, then places it.

` + sourceOrderHelp,
	Example: `  condatainer config source move lab --first
  condatainer config source move lab --before site`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: sourceNameCompletion,
	SilenceUsage:      true,
	RunE:              runSourceMove,
}

var configSourceListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List recipe sources in search order",
	Long: `List recipe sources in search order, with the layer each comes from.

- A source is private when it is read with a token, or is a directory.
- Under each source, its registry and the credential its prebuilt pulls send first.
- Tokens are never shown.`,
	Args:         cobra.NoArgs,
	SilenceUsage: true,
	RunE:         runSourceList,
}

var configSourceRemoveCmd = &cobra.Command{
	Use:     "remove [flags] <name>",
	Aliases: []string{"rm"},
	Short:   "Remove a recipe source and its token",
	Long: `Remove a recipe source from its config layer, with its recipe token.

- Its registry token goes with it. Registry logins stay; ` + "`condatainer registry logout`" + ` removes them.
- Removing cnt from a layer returns it to the default, searched last.`,
	Example:           `  condatainer config source remove lab`,
	Args:              cobra.ExactArgs(1),
	ValidArgsFunction: sourceNameCompletion,
	SilenceUsage:      true,
	RunE:              runSourceRemove,
}

func init() {
	for _, c := range []*cobra.Command{configSourceAddCmd, configSourceMoveCmd} {
		c.Flags().BoolVar(&sourceOpts.first, "first", false, "Put it first in its layer")
		c.Flags().BoolVar(&sourceOpts.last, "last", false, "Put it last in its layer")
		c.Flags().StringVar(&sourceOpts.before, "before", "", "Put it just before this source")
		c.Flags().StringVar(&sourceOpts.after, "after", "", "Put it just after this source")
		c.MarkFlagsMutuallyExclusive("first", "last", "before", "after")
		_ = c.RegisterFlagCompletionFunc("before", sourceNamesCompletion)
		_ = c.RegisterFlagCompletionFunc("after", sourceNamesCompletion)
	}
	configSourceAddCmd.Flags().BoolVar(&sourceOpts.tokenStdin, "token-stdin", false, "Read tokens from stdin, one per line: the recipe token, then the registry token")
	for _, c := range []*cobra.Command{configSourceAddCmd, configSourceMoveCmd, configSourceRemoveCmd} {
		c.Flags().StringVarP(&setLayer, "layer", "l", "", "Config layer to write: u/user, e/extra-root, r/app-root")
	}
	configSourceCmd.AddCommand(configSourceAddCmd, configSourceMoveCmd, configSourceListCmd, configSourceRemoveCmd)
	configCmd.AddCommand(configSourceCmd)
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

func runSourceMove(cmd *cobra.Command, args []string) error {
	name, pos := args[0], sourcePosition()
	if pos == (config.Position{}) {
		return fmt.Errorf("give one of --first, --last, --before, --after")
	}
	path, layer, err := holdingLayer(name)
	if err != nil {
		return err
	}
	list := config.LayerSourceList(path)
	spec, found := catalog.Spec{}, false
	for _, s := range list {
		if s.Name == name {
			spec, found = s, true
		}
	}
	if !found {
		if name != config.DefaultSource().Name {
			return fmt.Errorf("no source named %s in the %s layer", name, utils.StyleName(layer))
		}
		spec = config.DefaultSource()
	}
	placed, err := placeIn(list, spec, pos, layer)
	if err != nil {
		return err
	}
	if err := config.WriteLayerSourceList(path, placed); err != nil {
		return err
	}
	utils.PrintMessage("Moved source %s (%s layer)", name, utils.StyleName(layer))
	return nil
}

func runSourceRemove(cmd *cobra.Command, args []string) error {
	name := args[0]
	path, layer, err := holdingLayer(name)
	if err != nil {
		return err
	}
	list := config.LayerSourceList(path)
	var kept []catalog.Spec
	var removed *catalog.Spec
	for _, s := range list {
		if s.Name == name {
			removed = &s
			continue
		}
		kept = append(kept, s)
	}
	if removed == nil {
		if name == config.DefaultSource().Name {
			return fmt.Errorf("cnt is the default source and cannot be removed; `--source` leaves it out of one run")
		}
		return fmt.Errorf("no source named %s in the %s layer", name, utils.StyleName(layer))
	}
	if err := config.WriteLayerSourceList(path, kept); err != nil {
		return err
	}
	if file, err := config.CredentialFile(layer); err == nil {
		dropToken(file, credential.TrimScheme(removed.Base))
	}
	utils.PrintMessage("Removed source %s (%s layer)", name, utils.StyleName(layer))
	return nil
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

// dropToken removes a source's tokens from a layer, if it holds them.
func dropToken(file credential.File, key string) {
	if err := credential.Remove(file, credential.Source, key); err != nil && !errors.Is(err, credential.ErrNotStored) {
		utils.PrintWarning("Could not remove the token for %s: %v", key, err)
	}
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
