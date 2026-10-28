package report

import "github.com/condatainer/condatainer/internal/artifact/meta"

// Remote is a registry location a project's lock records for an overlay.
type Remote struct {
	Repository     string `json:"repository"`
	ManifestDigest string `json:"manifest_digest"`
}

// ProjectStep is a Step of a project's lock.
//   - Identity is the locked one; Found is the identity that stands in for it, absent for an exact match.
//   - Artifact is the vendored directory. DependsOn names artifacts too, since two pins can share an overlay name.
//   - Destination is the project path of a path pin, and Replaces what that path holds now.
//   - Transient is an overlay made only to build another and discarded; it has no Path.
//   - Cause is the artifact that failed beneath a skipped step. WaitsFor are the job IDs a submitted step runs after.
type ProjectStep struct {
	Step
	Artifact     string      `json:"artifact"`
	Requests     []string    `json:"requests,omitempty"`
	Direct       bool        `json:"direct"`
	Destination  string      `json:"destination,omitempty"`
	Replaces     string      `json:"replaces,omitempty"`
	Found        meta.KeyRef `json:"found,omitzero"`
	Remotes      []Remote    `json:"remotes,omitempty"`
	StoreOnly    bool        `json:"store_only,omitempty"`
	Unbuildable  bool        `json:"unbuildable,omitempty"`
	Transient    bool        `json:"transient,omitempty"`
	Diffs        []string    `json:"diffs,omitempty"`
	PayloadDrift bool        `json:"payload_drift,omitempty"`
	Rejected     string      `json:"rejected,omitempty"`
	Cause        string      `json:"cause,omitempty"`
	Queued       bool        `json:"queued,omitempty"`
	WaitsFor     []string    `json:"waits_for,omitempty"`
}

// ProjectSteps is the document of a command that makes a project's overlays available.
// Match is how a stand-in is accepted: identity or equivalence.
type ProjectSteps struct {
	Doc
	Root     string            `json:"root"`
	Match    string            `json:"match"`
	Steps    List[ProjectStep] `json:"steps"`
	Problems List[Problem]     `json:"problems"`
}

// ProjectPush is a Push of a project's lock.
//   - Digest is the manifest the lock records for it, and Tags are empty for a copy that is already there.
//   - Served is where a recipe source already publishes the overlay, so nothing is uploaded.
//   - Channels are the Conda channels a build was solved from, reported and never judged.
type ProjectPush struct {
	Push
	Artifact    string   `json:"artifact"`
	Destination string   `json:"destination,omitempty"`
	Channels    []string `json:"channels,omitempty"`
	Served      *Remote  `json:"served,omitempty"`
}

// ProjectPushes is the document of a command that publishes a project's overlays.
// AmbiguousNames are overlay names two pins share, so neither takes the plain tag.
type ProjectPushes struct {
	Doc
	Root           string            `json:"root"`
	Repository     string            `json:"repository,omitempty"`
	Audience       string            `json:"audience,omitempty"`
	Source         string            `json:"source,omitempty"`
	AmbiguousNames List[string]      `json:"ambiguous_names"`
	Pushes         List[ProjectPush] `json:"pushes"`
	Problems       List[Problem]     `json:"problems"`
}

// Finding is a line of a script the scan could not read as a declaration.
type Finding struct {
	Script string `json:"script"`
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// Declaration is what the scripts ask for. Artifact is the vendored directory
// pinned for this machine, absent when nothing is pinned. Pinnable is false for
// a declaration nothing can pin.
type Declaration struct {
	Request  string   `json:"request"`
	Kind     string   `json:"kind"`
	Scripts  []string `json:"scripts"`
	Pinnable bool     `json:"pinnable"`
	Artifact string   `json:"artifact,omitempty"`
}

// ProjectPin is one pin of a lock. Overlay, Identity and Equiv come from the
// vendored record, and are absent when it cannot be read. Artifacts maps each
// architecture the pin covers to its vendored directory. Manual is a pin no
// script declares.
type ProjectPin struct {
	Overlay
	Request   string            `json:"request"`
	Identity  meta.KeyRef       `json:"identity,omitzero"`
	Equiv     meta.KeyRef       `json:"equiv,omitzero"`
	Artifacts map[string]string `json:"artifacts"`
	Manual    bool              `json:"manual"`
}

// ProjectPins is the document of a command that lists a project's pins.
type ProjectPins struct {
	Doc
	Root     string           `json:"root"`
	Pins     List[ProjectPin] `json:"pins"`
	Problems List[Problem]    `json:"problems"`
}

// ProjectValidation is the document of a command that checks a project.
// Valid is false while there is any problem, finding or unpinned declaration.
type ProjectValidation struct {
	Doc
	Root             string            `json:"root"`
	Valid            bool              `json:"valid"`
	Scripts          List[string]      `json:"scripts"`
	Findings         List[Finding]     `json:"findings"`
	Unpinned         List[Declaration] `json:"unpinned"`
	SymlinkedScripts []string          `json:"symlinked_scripts,omitempty"`
	SymlinkedDirs    []string          `json:"symlinked_dirs,omitempty"`
	Problems         List[Problem]     `json:"problems"`
}

// ProjectLock is the document of a command that pins a project's declarations.
//   - Base is the root pinned for this machine.
//   - A declaration with no Artifact was not pinned, and its reason is in Problems.
//   - UnpinnedHelperOverlays are overlays helpers used that the project does not pin.
//   - ManualPinUsage maps a manual pin to the helpers that use it.
type ProjectLock struct {
	Doc
	Root                   string              `json:"root"`
	Base                   string              `json:"base,omitempty"`
	Requests               List[Declaration]   `json:"requests"`
	Findings               List[Finding]       `json:"findings"`
	UnpinnedHelperOverlays []string            `json:"unpinned_helper_overlays,omitempty"`
	ManualPinUsage         map[string][]string `json:"manual_pin_usage,omitempty"`
	SymlinkedScripts       []string            `json:"symlinked_scripts,omitempty"`
	SymlinkedDirs          []string            `json:"symlinked_dirs,omitempty"`
	Problems               List[Problem]       `json:"problems"`
}
