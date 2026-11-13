package hostrun

import (
	"fmt"
	"slices"

	"github.com/condatainer/condatainer/internal/scheduler"
	"github.com/condatainer/condatainer/internal/settings"
)

// The launches that can open the relay.
const (
	KindHelper = "helper"
	KindExec   = "exec"
	KindRun    = "run"
	KindNone   = "none"
)

// Where a command can run besides a named node.
const (
	OnLocalhost = "localhost"
	OnLogin     = "login"
)

var (
	keyLaunch = settings.List("host_run.launch",
		settings.DefaultList(KindHelper), settings.Order(1),
		settings.Validate(validateKind),
		settings.Accepts("a list of helper, exec, run, or none"),
		settings.Suggest(KindHelper, KindExec, KindRun, KindNone),
		settings.Help("Launches whose container can run commands on the host with host-run: helper, exec, run, or none for no launch."))

	keyOn = settings.String("host_run.on",
		settings.Default(OnLocalhost), settings.Order(2),
		settings.Validate(validateOn),
		settings.Accepts("localhost, login, or a node name"),
		settings.Suggest(OnLocalhost, OnLogin),
		settings.Help("Where host-run runs a command: localhost, login (the node the job was submitted from), or a node name."))
)

func validateKind(kind string) error {
	switch kind {
	case KindHelper, KindExec, KindRun, KindNone:
		return nil
	}
	return fmt.Errorf("%q is not a launch: use helper, exec, run or none", kind)
}

func validateOn(on string) error {
	if on == OnLocalhost || on == OnLogin {
		return nil
	}
	return validateNode(on)
}

// validateNode accepts a plain host name. A node is passed to ssh, so nothing it reads as an option is allowed.
func validateNode(node string) error {
	return scheduler.ValidateNodeNames([]string{node})
}

// Enabled reports whether a launch of the given kind opens the relay. None anywhere in the list turns every launch off.
func Enabled(kind string) bool {
	launches := keyLaunch.Get()
	return !slices.Contains(launches, KindNone) && slices.Contains(launches, kind)
}

// On is the node a command runs on when a request names none.
func On() string { return keyOn.Get() }
