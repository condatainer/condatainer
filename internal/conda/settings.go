package conda

import "github.com/condatainer/condatainer/internal/settings"

var keyChannels = settings.List("channels",
	settings.DefaultList("conda-forge", "bioconda"), settings.Order(7),
	settings.Help("Conda channels, highest priority first. The highest-priority file that sets it wins."))

// Channels returns the configured conda channels, highest priority first.
func Channels() []string { return keyChannels.Get() }
