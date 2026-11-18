package cmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/condatainer/condatainer/internal/project/lock"
	"github.com/condatainer/condatainer/internal/report"
	"github.com/condatainer/condatainer/internal/utils"
	"github.com/spf13/cobra"
)

func newProjectListCmd() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List pins",
		Long: `List every pin and the overlay it names.

- Works in a fresh clone that has restored nothing.
- To check that the overlays are available, use ` + "`project restore --dry-run`" + `.
- A 'manual' pin is one no script declares.
- A 'slot' pin is one only a #SLOT: names.
- ` + "`project lock`" + ` keeps it, and ` + "`project unpin`" + ` removes it.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := projectRoot(!jsonOutput)
			if err != nil {
				return err
			}
			l, err := lock.Load(root)
			if err != nil {
				return err
			}
			// Problems are for `project validate` to report; here an entry that
			// does not verify is one line that says so, so a broken pin is still
			// listed with the key that addresses it.
			verified, _ := lock.Verify(root, l)
			pins := pinReports(l, verified)
			markSlotOnly(root, pins)

			if jsonOutput {
				return writePins(cmd, root, pins)
			}
			if len(pins) == 0 {
				utils.PrintMessage("This project pins nothing.")
				utils.PrintHint("Run `condatainer project lock` to pin what its scripts declare.")
				return nil
			}
			// Rows go to stdout so the listing can be piped; the summary and
			// any warning stay on the console, which is stderr.
			width := 0
			for _, pin := range pins {
				width = max(width, len(pinLabel(pin)))
			}
			var manual, slotOnly int
			for _, pin := range pins {
				label := pinLabel(pin)
				// Padded from the unstyled label: styling adds escape bytes that
				// a width verb would count as columns.
				row := "  " + utils.StyleName(label) + strings.Repeat(" ", width-len(label))
				switch {
				case pin.Problem != "":
					row += "  " + utils.StyleWarning("unreadable")
				case pin.Manual:
					manual++
					row += "  " + identityPrefix(pin.Identity) + "  " + archList(pin) + "  " + utils.StyleWarning("manual")
				case pin.SlotOnly:
					slotOnly++
					row += "  " + identityPrefix(pin.Identity) + "  " + archList(pin) + "  " + utils.StyleDim("slot")
				default:
					row += "  " + identityPrefix(pin.Identity) + "  " + archList(pin)
				}
				fmt.Println(row)
				if pin.Problem != "" {
					utils.PrintWarning("  %s", pin.Problem)
				}
			}
			summary := fmt.Sprintf("%d pin(s)", len(pins))
			if manual > 0 {
				summary += fmt.Sprintf(", %d manual", manual)
			}
			if slotOnly > 0 {
				summary += fmt.Sprintf(", %d slot", slotOnly)
			}
			utils.PrintMessage("%s", summary)
			return nil
		},
	}
	jsonFlag(cmd.Flags(), &jsonOutput)
	return cmd
}

// markSlotOnly marks the pins that only a #SLOT: names, which the lock does not record.
//   - It reads the scripts, so a clone with no scripts, or a scan that fails, marks nothing.
func markSlotOnly(root string, pins []pinReport) {
	scanned, err := lock.Scan(root, lock.ScanOptions{})
	if err != nil {
		return
	}
	slotOnly := map[string]bool{}
	for _, request := range scanned.Requests {
		if request.SlotOnly {
			for _, key := range request.PathCandidates() {
				slotOnly[key] = true
			}
		}
	}
	for i := range pins {
		pins[i].SlotOnly = slotOnly[pins[i].Request] && !pins[i].Manual
	}
}

// archList names the architectures a pin has an artifact for.
func archList(pin pinReport) string {
	return strings.Join(slices.Sorted(maps.Keys(pin.Artifacts)), ",")
}

// pinLabel is the pin key, and the artifact name after it when the key does not
// already carry one — a path addresses a file, which says nothing about what is
// in it, while a name request is the name.
func pinLabel(pin pinReport) string {
	if pin.Request == lock.BaseKey {
		if pin.Name == "" {
			return "base"
		}
		return "base (" + pin.Name + ")"
	}
	if pin.Name == "" || pin.Name == pin.Request {
		return pin.Request
	}
	return pin.Request + " (" + pin.Name + ")"
}

// pinsDoc is a project's pins as the pins document. A pin whose records do not
// verify is listed, with its reason in problems.
func writePins(cmd *cobra.Command, root string, pins []pinReport) error {
	return report.Write(cmd.OutOrStdout(), pinsDoc(root, pins))
}

func pinsDoc(root string, pins []pinReport) *report.ProjectPins {
	doc := &report.ProjectPins{Root: root, Pins: report.List[report.ProjectPin]{}, Problems: report.List[report.Problem]{}}
	for _, pin := range pins {
		record := report.ProjectPin{Overlay: report.Overlay{Name: pin.Name}, Request: pin.Request,
			Artifacts: pin.Artifacts, Manual: pin.Manual, SlotOnly: pin.SlotOnly}
		if pin.Entry != nil {
			record.Type, record.Identity, record.Equiv = pin.Entry.Manifest.Type, pin.Entry.Identity, pin.Entry.Equiv
		}
		doc.Pins = append(doc.Pins, record)
		if pin.Problem != "" {
			doc.Problems = append(doc.Problems, report.Problem{Name: pin.Request, Message: pin.Problem})
		}
	}
	return doc
}
