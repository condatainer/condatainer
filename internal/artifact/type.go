// Package artifact holds what an installed image is and records. The subpackages
// read and compare its manifest, key it and cache it; this package holds only the
// type every one of them shares.
package artifact

// Type is what a payload is, and what a runtime does with it. It is what a
// recipe declares as #TYPE: and what an image records as its manifest `type`.
//
// Go reserves `type`, so a local or parameter holding one is spelled `typ`.
type Type string

const (
	TypeOS   Type = "os"   // adds to a root that already exists; any os can be the container root, chosen per invocation
	TypeApp  Type = "app"  // contributes to PATH
	TypeData Type = "data" // does not
	// TypeEnv is what `overlay freeze` captures from a writable overlay. No
	// recipe may declare it: DeriveType never returns it and Recipe.Validate
	// rejects it. It stacks at the container root like an os, but is its own
	// type because type carries publishing defaults and an undeclared os may be
	// published publicly. Written "environment" in anything a user reads.
	TypeEnv Type = "env"
)
