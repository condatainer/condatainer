package utils

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// EnvRealHome holds the user's own home once home_override has replaced HOME.
const EnvRealHome = "CNT_REAL_HOME"

// RealHome returns the user's own home: CNT_REAL_HOME when HOME was replaced, else HOME.
func RealHome() (string, error) {
	if home := os.Getenv(EnvRealHome); home != "" {
		return home, nil
	}
	return os.UserHomeDir()
}

// ReplaceHome sets HOME to dir, first saving the current home in CNT_REAL_HOME.
// A home already saved is kept, so replacing twice changes nothing.
func ReplaceHome(dir string) {
	if os.Getenv(EnvRealHome) == "" {
		if home, err := os.UserHomeDir(); err == nil {
			os.Setenv(EnvRealHome, home) //nolint:errcheck
		}
	}
	os.Setenv("HOME", dir) //nolint:errcheck
}

// withHomeHint adds the home_override hint to a write error under an unreplaced HOME that is read-only or not permitted.
func withHomeHint(err error, path string) error {
	if err == nil || os.Getenv(EnvRealHome) != "" {
		return err
	}
	home := os.Getenv("HOME")
	if home == "" || !strings.HasPrefix(path, strings.TrimRight(home, "/")+"/") {
		return err
	}
	if !errors.Is(err, syscall.EROFS) && !errors.Is(err, syscall.EACCES) {
		return err
	}
	return fmt.Errorf("%w (HOME is not writable here; set home_override in the config)", err)
}
