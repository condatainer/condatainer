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
// for. Digest and Identity are set when the registry was asked for the copy, and absent when a tag list answered.
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
// this file is not the one used. Keys are the ones its manifest records, and
// Runtime is the image's own runtime document; each is absent when it could not
// be read.
type Installed struct {
	Overlay
	Alias string `json:"alias,omitempty"`
	Location
	Shadowed bool          `json:"shadowed"`
	Keys     *meta.Keys    `json:"keys,omitempty"`
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

// Dependency is one #DEP: of a script. Request is as the script wrote it, and
// Name is what an installed one is called. An installed one has its Location.
type Dependency struct {
	Request   string `json:"request"`
	Name      string `json:"name,omitempty"`
	Installed bool   `json:"installed"`
	Location
}

// Script is what a script, or a recipe given by name, declares.
type Script struct {
	Script string           `json:"script"`
	Deps   List[Dependency] `json:"deps"`
}

// Checked is the document of a command that checks what scripts declare.
// Missing is every request that is not installed, once, over all scripts.
// Steps is present with a dry run of -a: what installing the missing would do.
type Checked struct {
	Doc
	Scripts  List[Script]  `json:"scripts"`
	Missing  List[string]  `json:"missing"`
	Steps    List[Step]    `json:"steps,omitempty"`
	Problems List[Problem] `json:"problems"`
}

// Why an overlay was left in place by a removal.
const (
	KeptInUse     = "in_use"    // an exclusive lock was refused: something is running it
	KeptProtected = "protected" // its directory is not writable
	KeptNotFound  = "not_found" // nothing installed answers the name
	KeptAmbiguous = "ambiguous" // installed in several layers, and none was chosen
	KeptFailed    = "failed"    // the removal itself failed; Error says why
)

// Removed is an overlay a removal took, or in a dry run would take.
type Removed struct {
	Overlay
	Location
}

// Kept is something a removal left in place, and why.
type Kept struct {
	Overlay
	Location
	Reason string `json:"reason"`
	Error  string `json:"error,omitempty"`
}

// Removal is the document of a removal that acted.
type Removal struct {
	Doc
	Removed  List[Removed] `json:"removed"`
	Kept     List[Kept]    `json:"kept"`
	Problems List[Problem] `json:"problems"`
}

// RemovalPlan is the document of a removal run with --dry-run: what it would
// take. Nothing is deleted.
type RemovalPlan struct {
	Doc
	WouldRemove List[Removed] `json:"would_remove"`
	Kept        List[Kept]    `json:"kept"`
	Problems    List[Problem] `json:"problems"`
}

// Move is a tag moving from one build to another. From and To name the builds:
// by build tag in a registry removal, and by leading hex in a push.
type Move struct {
	Tag  string `json:"tag"`
	From string `json:"from"`
	To   string `json:"to,omitempty"`
}

// Push is one overlay a push publishes, or in a preview would.
//   - Path is the local file, and Tags are the tags the push writes: none, with
//     Existing, when everything is already there.
//   - Digest is the published manifest. It is known once the build is there.
//   - Moved is the name tag leaving an older build.
//   - Outcome is empty in a preview. A refusal before anything moved is
//     refused, and a push that started and stopped is failed; Error says why.
type Push struct {
	Overlay
	Path       string      `json:"path"`
	Repository string      `json:"repository,omitempty"`
	Identity   meta.KeyRef `json:"identity,omitzero"`
	Equiv      meta.KeyRef `json:"equiv,omitzero"`
	Digest     string      `json:"digest,omitempty"`
	Reference  string      `json:"reference,omitempty"`
	Tags       []string    `json:"tags,omitempty"`
	Size       int64       `json:"size,omitempty"`
	Audience   string      `json:"audience,omitempty"`
	Existing   bool        `json:"existing"`
	Moved      *Move       `json:"moved,omitempty"`
	Outcome    Outcome     `json:"outcome,omitempty"`
	Error      string      `json:"error,omitempty"`
}

// Pushes is the document of a command that publishes overlays.
type Pushes struct {
	Doc
	Pushes   List[Push]    `json:"pushes"`
	Problems List[Problem] `json:"problems"`
}

// RegistryManifest is a manifest in a registry with every tag on it. Name,
// Arch and the keys come from its annotations, and are absent when they cannot
// be read.
type RegistryManifest struct {
	Overlay
	Digest   string      `json:"digest"`
	Identity meta.KeyRef `json:"identity,omitzero"`
	Equiv    meta.KeyRef `json:"equiv,omitzero"`
	Tags     []string    `json:"tags,omitempty"`
}

// Why a registry removal left a manifest.
const (
	KeptForeignTag = "foreign_tag" // it holds a tag this tool did not write
	KeptChanged    = "changed"     // it changed after the list was made
)

// RegistryKept is a manifest a registry removal left, and why.
type RegistryKept struct {
	Digest string   `json:"digest"`
	Tags   []string `json:"tags,omitempty"`
	Reason string   `json:"reason"`
}

// RegistryRemoval is the document of a registry removal that acted. Moved are
// the name tags that moved to another build first.
type RegistryRemoval struct {
	Doc
	Repository string                 `json:"repository"`
	Removed    List[RegistryManifest] `json:"removed"`
	Moved      List[Move]             `json:"moved"`
	Kept       List[RegistryKept]     `json:"kept"`
	Problems   List[Problem]          `json:"problems"`
}

// RegistryRemovalPlan is the same removal reported and not made.
type RegistryRemovalPlan struct {
	Doc
	Repository  string                 `json:"repository"`
	WouldRemove List[RegistryManifest] `json:"would_remove"`
	WouldMove   List[Move]             `json:"would_move"`
	Kept        List[RegistryKept]     `json:"kept"`
	Problems    List[Problem]          `json:"problems"`
}

// Stored is an overlay in an images directory, either under its plain name
// (layout flat) or filed by identity (layout store). Keys are its identity and
// equivalence, regenerated from the sources it embeds.
type Stored struct {
	Overlay
	Location
	Keys    *meta.Keys    `json:"keys,omitempty"`
	Runtime *meta.Runtime `json:"runtime,omitempty"`
}

// Added is the document of adding an overlay file to the store. Added is false
// when the build was already installed, and the record is where it is.
type Added struct {
	Doc
	Overlay  Stored        `json:"overlay"`
	Added    bool          `json:"added"`
	Problems List[Problem] `json:"problems"`
}

// Promotion is the document of making one build the one its name resolves to.
//   - Overlay is that build. Demoted is the build that held the name, now in
//     the store, and Shadows is a farther copy of the name this one overrules.
//   - AlreadyCurrent says the build already answered to the name.
//   - Outcome is done once it is promoted, and absent in a preview, where
//     Overlay is the build as it is filed now.
type Promotion struct {
	Doc
	Overlay        Stored        `json:"overlay"`
	Demoted        *Stored       `json:"demoted,omitempty"`
	Shadows        string        `json:"shadows,omitempty"`
	AlreadyCurrent bool          `json:"already_current,omitempty"`
	Outcome        Outcome       `json:"outcome,omitempty"`
	Problems       List[Problem] `json:"problems"`
}

// Collected is a store entry a garbage collection took, or would take. Age is
// in seconds from the newest of the access, modification and change times, and
// Basis names the one that decided it. Staging marks an abandoned partial file.
type Collected struct {
	Overlay
	Location
	Identity meta.KeyRef `json:"identity,omitzero"`
	Age      int64       `json:"age"`
	Basis    string      `json:"basis"`
	Staging  bool        `json:"staging,omitempty"`
}

// CollectKept is a store entry a garbage collection left, with the reason it
// gives in words.
type CollectKept struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Collection is the document of a store garbage collection that acted. Grace
// is in seconds, and Reclaimed is the bytes freed.
type Collection struct {
	Doc
	Grace     int64             `json:"grace"`
	Reclaimed int64             `json:"reclaimed"`
	Removed   List[Collected]   `json:"removed"`
	Kept      List[CollectKept] `json:"kept"`
	Problems  List[Problem]     `json:"problems"`
}

// CollectionPlan is the same collection reported and not made.
type CollectionPlan struct {
	Doc
	Grace       int64             `json:"grace"`
	Reclaimable int64             `json:"reclaimable"`
	WouldRemove List[Collected]   `json:"would_remove"`
	Kept        List[CollectKept] `json:"kept"`
	Problems    List[Problem]     `json:"problems"`
}
