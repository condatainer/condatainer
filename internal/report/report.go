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

// Check is the outcome of one --validate check.
type Check string

const (
	CheckOK          Check = "ok"
	CheckFailed      Check = "failed"
	CheckNotRecorded Check = "not_recorded"
)

// Checks are the --validate results. Errors say why a check failed.
type Checks struct {
	Keys    Check    `json:"keys"`
	Payload Check    `json:"payload"`
	Errors  []string `json:"errors,omitempty"`
}

// Conda is the Conda history inside an image: the channels and the packages
// that were asked for.
type Conda struct {
	Channels []string `json:"channels,omitempty"`
	Packages []string `json:"packages"`
}

// SquashFS is what the archive's superblock says.
type SquashFS struct {
	Compression       string `json:"compression"`
	CompressionLevel  int    `json:"compression_level,omitempty"`
	BlockSize         int64  `json:"block_size"`
	Inodes            int64  `json:"inodes"`
	Fragments         int64  `json:"fragments"`
	DuplicatesRemoved bool   `json:"duplicates_removed,omitempty"`
	Created           string `json:"created,omitempty"`
}

// Filesystem is what an ext3 image's superblock says. Format is what the
// filesystem inside reports, which may differ from the Location's format.
type Filesystem struct {
	Format      string `json:"format"`
	State       string `json:"state"`
	BlockSize   int64  `json:"block_size"`
	Created     string `json:"created,omitempty"`
	Modified    string `json:"modified,omitempty"`
	LastMounted string `json:"last_mounted,omitempty"`
}

// Owner is who owns the files inside an ext3 image. Fakeroot is set when they
// are owned by root.
type Owner struct {
	UID      *int `json:"uid,omitempty"`
	GID      *int `json:"gid,omitempty"`
	Fakeroot bool `json:"fakeroot,omitempty"`
}

// Usage is how full an ext3 image is, in bytes and inodes.
type Usage struct {
	Used        int64 `json:"used"`
	Total       int64 `json:"total"`
	InodesUsed  int64 `json:"inodes_used"`
	InodesTotal int64 `json:"inodes_total"`
}

// Info is everything known about one overlay file, read from the file and
// never regenerated. A field is present only when the image has it: Recipe for
// a script or definition build, Conda for a Conda environment, and the ext3
// blocks for an .img, which has no Name, Arch, Keys, Manifest or Runtime.
// OtherFiles says the payload holds an entry other than /cnt_env.
type Info struct {
	Overlay
	Location
	SizeOnDisk int64          `json:"size_on_disk,omitempty"`
	Distro     string         `json:"distro,omitempty"`
	Keys       *meta.Keys     `json:"keys,omitempty"`
	Manifest   *meta.Manifest `json:"manifest,omitempty"`
	Runtime    *meta.Runtime  `json:"runtime,omitempty"`
	Recipe     string         `json:"recipe,omitempty"`
	Conda      *Conda         `json:"conda,omitempty"`
	OtherFiles *bool          `json:"other_files,omitempty"`
	SquashFS   *SquashFS      `json:"squashfs,omitempty"`
	Filesystem *Filesystem    `json:"filesystem,omitempty"`
	Owner      *Owner         `json:"owner,omitempty"`
	Usage      *Usage         `json:"usage,omitempty"`
}

// Inspection is the document of a command that describes one overlay file.
// Checks is present only after --validate.
type Inspection struct {
	Doc
	Overlay  Info          `json:"overlay"`
	Checks   *Checks       `json:"checks,omitempty"`
	Problems List[Problem] `json:"problems"`
}

// Template is the placeholder part of a recipe. A template lists every value of
// each placeholder in Values, with Order giving the placeholder order in the
// target. A variant has the one value it was expanded with in Placeholders.
type Template struct {
	Target       string              `json:"target"`
	Order        []string            `json:"order,omitempty"`
	Values       map[string][]string `json:"values,omitempty"`
	Placeholders map[string]string   `json:"placeholders,omitempty"`
}

// Recipe is something a source can build. Installed says an overlay answers its
// name, and Buildable that this machine is one of BuildsOn, or any machine is
// when BuildsOn is empty. Recipe is its path in the source.
type Recipe struct {
	Overlay
	Alias       string    `json:"alias,omitempty"`
	Installed   bool      `json:"installed"`
	Buildable   bool      `json:"buildable"`
	Source      string    `json:"source,omitempty"`
	Recipe      string    `json:"recipe,omitempty"`
	Description string    `json:"description,omitempty"`
	URL         string    `json:"url,omitempty"`
	Deps        []string  `json:"deps,omitempty"`
	BuildsOn    []string  `json:"builds_on,omitempty"`
	Template    *Template `json:"template,omitempty"`
}

// Recipes is the document of a command that lists recipes.
type Recipes struct {
	Doc
	Recipes  List[Recipe]  `json:"recipes"`
	Problems List[Problem] `json:"problems"`
}
