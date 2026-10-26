package container

import (
	"errors"
	"os"

	"github.com/condatainer/condatainer/internal/utils"
)

// Headers is what the #BIND:, #WRITABLE: and #FAKEROOT: headers of a script ask of the container.
type Headers struct {
	// Binds are the #BIND: values in the order written, with $VAR and ${VAR} expanded.
	Binds    []string
	Writable bool
	Fakeroot bool
}

// ParseHeaders reads the container headers from a script's annotations.
//   - #BIND: may repeat, and an empty one is skipped.
//   - #WRITABLE: and #FAKEROOT: are flags: on when present, and any value but true is an error naming its line.
//   - A bind expands against the environment of the process that starts the container, so a submitted job expands on the node it runs on.
func ParseHeaders(annotations []utils.Annotation) (Headers, error) {
	var h Headers
	for _, a := range utils.SelectAnnotations(annotations, "#BIND") {
		if a.Value != "" {
			h.Binds = append(h.Binds, os.ExpandEnv(a.Value))
		}
	}
	var err1, err2 error
	h.Writable, err1 = utils.Flag(annotations, "#WRITABLE")
	h.Fakeroot, err2 = utils.Flag(annotations, "#FAKEROOT")
	return h, errors.Join(err1, err2)
}
