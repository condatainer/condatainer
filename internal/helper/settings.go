package helper

import "github.com/condatainer/condatainer/internal/settings"

// How a service on a compute node is reached.
const (
	ConnectAuto      = "auto"      // SSH, then the scheduler's own way into the job
	ConnectSSH       = "ssh"       // SSH tunnel only
	ConnectScheduler = "scheduler" // the scheduler's own way into the job only
	ConnectDirect    = "direct"    // service binds all interfaces; connect to node:port
)

var (
	keyConnect = settings.Enum("helper.connect",
		settings.Values(ConnectAuto, ConnectSSH, ConnectScheduler, ConnectDirect),
		settings.Default(ConnectAuto), settings.Order(1),
		settings.Help("How the dashboard reaches a helper on a compute node. Only direct makes the service bind to all interfaces."))

	keyNotification = settings.Enum("helper.notification",
		settings.Values("none", "terminal", "web", "both"),
		settings.AllowEmpty(),
		settings.Normalize(func(s string) string {
			if s == "" {
				return "none"
			}
			return s
		}),
		settings.Default("web"), settings.Order(2),
		settings.Help("How a helper tells you it is ready: a terminal bell, a browser notification in the dashboard, both, or none."))
)

// Connect is how the dashboard reaches a helper service.
func Connect() string { return keyConnect.Get() }

// Notification is how a helper reports that it is ready.
func Notification() string { return keyNotification.Get() }
