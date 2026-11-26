package server

import "github.com/condatainer/condatainer/internal/settings"

var (
	keyDownloadWarnSize = settings.MemoryMB("server.download_warn_size",
		settings.Default("5G"), settings.Order(1),
		settings.Help("The dashboard asks before downloading a zip larger than this."),
		settings.Suggest("1g", "5g", "20g", "100g"))

	keyDownloadWarnFiles = settings.Int("server.download_warn_files",
		settings.Default(10000), settings.Min(1), settings.Order(2),
		settings.Help("The dashboard asks before downloading a zip of more files than this."),
		settings.Suggest("5000", "10000", "20000", "100000"))

	keyRequireToken = settings.Bool("server.require_token",
		settings.Default(true), settings.Order(3),
		settings.Help("The dashboard asks for its token. Turn off only on a host that no other user can reach."))

	keyTokenDays = settings.Days("server.token_days",
		settings.Default(30), settings.Min(0), settings.Order(4),
		settings.Help("Days before the dashboard token is replaced and every browser signs in again. 0 never replaces it."),
		settings.Suggest("7", "30", "90", "0"))
)

func init() {
	keyTokenDays.SetShow(func(stored string) string {
		if stored == "0" {
			return "0 (disabled)"
		}
		return stored + "d"
	})
}
