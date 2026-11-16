package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/store"
	"github.com/spf13/cobra"
)

var storeCmd = &cobra.Command{
	Use:   "store",
	Short: "Inspect overlays kept under an exact build identity",
	Long: `Inspect and manage overlays kept under an exact build identity.

- It holds builds that could not take a plain name because another build had it.
- An entry is <name>@<identity>, so several builds of a name can coexist.
- Overlays under a plain name: use ` + "`condatainer list`" + ` and ` + "`condatainer remove`" + `.`,
}

func init() {
	rootCmd.AddCommand(storeCmd)
	storeCmd.AddCommand(newStoreAddCmd(), newStoreListCmd(), newStorePathCmd(), newStoreValidateCmd(),
		newStoreUseCmd(), newStoreRemoveCmd(), newStoreGCCmd())
}

// splitStoreAddress reads "<name>" or "<name>@<identity>", with --identity as the alternative spelling of the second half.
//   - It splits on the first @: a name never contains one and an identity may (scheme@sha256:...).
//   - The combined form lets a line of `store list` output be pasted into `store use`.
func splitStoreAddress(arg, flag string) (string, store.IdentityQuery, error) {
	name, inline, hasInline := strings.Cut(arg, "@")
	switch {
	case hasInline && flag != "" && inline != flag:
		return "", store.IdentityQuery{}, fmt.Errorf(
			"two identities given: %q in the address and %q in --identity", inline, flag)
	case !hasInline:
		inline = flag
	}
	if strings.TrimSpace(inline) == "" {
		return "", store.IdentityQuery{}, errors.New(
			"an identity is required: give it as <name>@<identity>, or with --identity")
	}
	query, err := store.ParseIdentityQuery(inline)
	if err != nil {
		return "", store.IdentityQuery{}, err
	}
	return name, query, nil
}

// storedRecord is the record of a verified store candidate. Its name and keys
// are the ones regenerated from the sources it embeds; its type and
// architecture come from its runtime document.
func storedRecord(c store.Candidate) report.Stored {
	return storedRecordWith(c, meta.ReadRuntime)
}

// storedRecordWith is storedRecord reading the runtime document through read,
// so a listing of many entries can read through a cache batch.
func storedRecordWith(c store.Candidate, read func(string) (meta.Runtime, error)) report.Stored {
	rec := report.Stored{
		Overlay:  report.Overlay{Name: c.Name},
		Location: plannedLocation(c.Path, true),
		Keys:     &meta.Keys{Identity: c.Identity, Equiv: c.Equiv},
	}
	rec.Layout, rec.Size = c.Layout, c.Size
	if rt, err := read(c.Path); err == nil {
		rec.Type, rec.Arch, rec.Runtime = rt.Type, rt.Platform.Arch, &rt
	}
	return rec
}
