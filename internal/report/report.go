// Package report is the JSON an overlay command prints with --json: one
// document on stdout, built from parts every such command shares.
package report

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/condatainer/condatainer/internal/artifact"
	"github.com/condatainer/condatainer/internal/artifact/meta"
	"github.com/condatainer/condatainer/internal/store"
	"github.com/condatainer/condatainer/internal/utils"
)

// SchemaVersion is the version every document is written at.
const SchemaVersion = 1

// Doc opens every document with its schema version. A document embeds it first.
type Doc struct {
	SchemaVersion int `json:"schema_version"`
}

func (d *Doc) stamp() { d.SchemaVersion = SchemaVersion }

// document is a type that embeds Doc.
type document interface{ stamp() }

// Write prints doc as the one JSON document of a run: indented by two spaces,
// with a trailing newline, and the schema version set.
func Write(w io.Writer, doc document) error {
	doc.stamp()
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(doc)
}

// List is a JSON array that is written as [] when empty, never null.
type List[T any] []T

// MarshalJSON encodes without HTML escaping, since the encoder Write uses
// keeps a marshaler's escapes as they are.
func (l List[T]) MarshalJSON() ([]byte, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode([]T(l)); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

// Problem is something that did not stop the command but left a record
// incomplete or out. Name is the overlay or input it is about.
type Problem struct {
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

// Overlay is what an overlay is. Every record about one overlay starts with it.
// Type and Arch come from the image, the registry or the recipe, never the name.
type Overlay struct {
	Name string        `json:"name,omitempty"`
	Type artifact.Type `json:"type,omitempty"`
	Arch string        `json:"arch,omitempty"`
}

// Format is what an overlay file holds.
type Format string

const (
	FormatSquashFS Format = "squashfs"
	FormatExt3     Format = "ext3"
	FormatSIF      Format = "sif"
)

// FormatOf reads a file's format from its extension, the way every command
// decides how to handle it. A file of no overlay format has none.
func FormatOf(path string) Format {
	switch {
	case utils.IsSqf(path):
		return FormatSquashFS
	case utils.IsImg(path):
		return FormatExt3
	case utils.IsSif(path):
		return FormatSIF
	}
	return ""
}

// Location is where an overlay is on disk. Layer is user, extra-root or cnt-root.
// Format is set with Path, by FormatOf.
type Location struct {
	Format Format       `json:"format,omitempty"`
	Path   string       `json:"path,omitempty"`
	Dir    string       `json:"dir,omitempty"`
	Layer  string       `json:"layer,omitempty"`
	Layout store.Layout `json:"layout,omitempty"`
	Size   int64        `json:"size,omitempty"`
}

// Action is what a plan chose to make an overlay available.
type Action string

const (
	ActionAdopt       Action = "adopt"       // already here, used as is
	ActionFetch       Action = "fetch"       // downloaded from a registry
	ActionBuild       Action = "build"       // built from the recipe
	ActionUnavailable Action = "unavailable" // neither here nor obtainable
)

// Runs is where a build runs.
type Runs string

const (
	RunsLocal Runs = "local"
	RunsJob   Runs = "job"
)

// Outcome is what happened to a step once the command acted.
type Outcome string

const (
	OutcomeDone      Outcome = "done"
	OutcomeSubmitted Outcome = "submitted"
	OutcomeSkipped   Outcome = "skipped"
	OutcomeFailed    Outcome = "failed"
	OutcomeRefused   Outcome = "refused"
)

// Dep is one #DEP: of a step, as the step's key counts it. Role is data (its
// equivalence enters the key), app (its name does) or history (nothing does).
// An installed one has its Location.
type Dep struct {
	Overlay
	Role      string      `json:"role"`
	Installed bool        `json:"installed"`
	Identity  meta.KeyRef `json:"identity,omitzero"`
	Equiv     meta.KeyRef `json:"equiv,omitzero"`
	Location
}

// Step is one overlay in a plan, and after acting, in a run.
//   - Endpoint, Digest and Identity describe the copy a fetch takes.
//   - Equiv is the recipe's key whenever it is known, whatever the action, and
//     Deps are the dependencies it was derived from.
//   - DependsOn orders the steps; Deps describes them.
//   - Outcome, JobID and Error are empty in a dry run.
type Step struct {
	Overlay
	Action        Action      `json:"action"`
	Runs          Runs        `json:"runs,omitempty"`
	Endpoint      string      `json:"endpoint,omitempty"`
	Digest        string      `json:"digest,omitempty"`
	Identity      meta.KeyRef `json:"identity,omitzero"`
	Equiv         meta.KeyRef `json:"equiv,omitzero"`
	DependsOn     []string    `json:"depends_on,omitempty"`
	Deps          []Dep       `json:"deps,omitempty"`
	RequiresInput bool        `json:"requires_input"`
	Reason        string      `json:"reason,omitempty"`
	Location
	Outcome Outcome `json:"outcome,omitempty"`
	JobID   string  `json:"job_id,omitempty"`
	Error   string  `json:"error,omitempty"`
}

// Steps is the document of a command that makes overlays available.
type Steps struct {
	Doc
	Steps    List[Step]    `json:"steps"`
	Problems List[Problem] `json:"problems"`
}

// Copy is whether a registry holds the copy a local recipe would make, for one
// architecture. Equiv is the recipe's key and Tag the equivalence tag asked
// for. A published copy adds the Digest and Identity its manifest says.
type Copy struct {
	Overlay
	Published bool        `json:"published"`
	Equiv     meta.KeyRef `json:"equiv"`
	Tag       string      `json:"tag"`
	Digest    string      `json:"digest,omitempty"`
	Identity  meta.KeyRef `json:"identity,omitzero"`
}

// Copies is the document of a command that checks overlays against a registry.
type Copies struct {
	Doc
	Repository string        `json:"repository"`
	Overlays   List[Copy]    `json:"overlays"`
	Problems   List[Problem] `json:"problems"`
}

// Tag is one tag of a registry. A tag this tool did not write has only Tag and
// Kind (other). Key is the short key a build or equivalence tag spells.
type Tag struct {
	Tag  string `json:"tag"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind"`
	Key  string `json:"key,omitempty"`
	Arch string `json:"arch,omitempty"`
}

// Tags is the document of a command that lists a registry's tags.
type Tags struct {
	Doc
	Repository string        `json:"repository"`
	Tags       List[Tag]     `json:"tags"`
	Problems   List[Problem] `json:"problems"`
}

// Resolved is a published copy found by an address, from its manifest
// annotations alone. Tag is the tag the address asked for, empty for a digest.
type Resolved struct {
	Overlay
	Tag      string      `json:"tag,omitempty"`
	Digest   string      `json:"digest"`
	Identity meta.KeyRef `json:"identity,omitzero"`
	Equiv    meta.KeyRef `json:"equiv,omitzero"`
}

// Resolution is the document of a command that finds one published copy.
type Resolution struct {
	Doc
	Overlay  Resolved      `json:"overlay"`
	Problems List[Problem] `json:"problems"`
}

// Installed is an overlay file on this machine. Alias is its short form under
// the default distro. Shadowed says a nearer directory holds the same name, so
// this file is not the one used. Runtime is the image's own runtime document,
// absent when it could not be read.
type Installed struct {
	Overlay
	Alias string `json:"alias,omitempty"`
	Location
	Shadowed bool          `json:"shadowed"`
	Runtime  *meta.Runtime `json:"runtime,omitempty"`
}

// Overlays is the document of a command that lists overlays, one record each.
type Overlays[T any] struct {
	Doc
	Overlays List[T]       `json:"overlays"`
	Problems List[Problem] `json:"problems"`
}
