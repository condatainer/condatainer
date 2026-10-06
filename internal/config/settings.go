package config

import (
	"time"

	"github.com/condatainer/condatainer/internal/settings"
)

var (
	keyDefaultDistro = settings.String("default_distro",
		settings.AllowEmpty(), settings.Order(1),
		settings.Help("The os overlay that is the container root, such as ubuntu24. Recorded from the first source that recommends one."))

	keyHomeOverride = settings.String("home_override",
		settings.AllowEmpty(), settings.OmitEmpty(), settings.Order(2),
		settings.Help("Directory that replaces HOME in every command and job, for clusters whose home is read-only on compute nodes. $VARIABLES are expanded."))

	keyMetadataCacheTTL = settings.Days("metadata_cache_ttl",
		settings.Default(1), settings.Min(0), settings.Order(5),
		settings.Help("Days to keep fetched recipe metadata. 0 turns the cache off."),
		settings.Suggest("1", "3", "7", "14", "0"))
)

func init() {
	keyMetadataCacheTTL.SetShow(func(stored string) string {
		if stored == "0" {
			return "0 (disabled)"
		}
		return stored + "d"
	})
}

// recommendedDistro is the distro a source recommended when none is configured.
var recommendedDistro string

// DefaultDistro is the configured default distro, else the one a source recommended. It never opens the catalog.
func DefaultDistro() string {
	if d := keyDefaultDistro.Get(); d != "" {
		return d
	}
	return recommendedDistro
}

// MetadataCacheTTL is how long fetched recipe metadata is kept. Zero turns the cache off.
func MetadataCacheTTL() time.Duration { return keyMetadataCacheTTL.Get() }

// HomeOverride is the configured home_override, before $VARIABLES are expanded.
func HomeOverride() string { return keyHomeOverride.Get() }
