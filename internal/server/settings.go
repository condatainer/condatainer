package server

import "github.com/condatainer/condatainer/internal/settings"

var (
	keyDownloadWarnSize = settings.MemoryMB("server.download_warn_size",
		settings.Default(5120), settings.Order(1),
		settings.Help("The dashboard asks before downloading a zip larger than this."),
		settings.Suggest("1g", "5g", "20g", "100g"))

	keyDownloadWarnFiles = settings.Int("server.download_warn_files",
		settings.Default(10000), settings.Min(1), settings.Order(2),
		settings.Help("The dashboard asks before downloading a zip of more files than this."),
		settings.Suggest("5000", "10000", "20000", "100000"))
)
