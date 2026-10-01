package registry

import (
	"testing"

	"github.com/condatainer/condatainer/internal/credential"
)

func TestLoginAndLogoutRejectATargetThatIsNotAHostOrRepository(t *testing.T) {
	useLayers(t, map[string]map[string]string{"user": {}})
	for _, target := range []string{"", "ghcr.io//lab", "ghcr.io/my lab", "https://ghcr.io/lab@x"} {
		if err := Login(t.Context(), target, "user", "user", "token"); err == nil {
			t.Errorf("Login(%q) accepted it", target)
		}
		if err := Logout(t.Context(), target, "user"); err == nil {
			t.Errorf("Logout(%q) accepted it", target)
		}
	}
}

func TestLogoutRemovesOnlyTheNamedKey(t *testing.T) {
	files := useLayers(t, map[string]map[string]string{"user": {
		"ghcr.io": "host", "ghcr.io/my-lab/rnaseq": "repo",
	}})
	if err := Logout(t.Context(), "ghcr.io/my-lab/rnaseq", "user"); err != nil {
		t.Fatal(err)
	}
	stored, err := credential.List([]credential.File{files["user"]}, credential.Registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Key != "ghcr.io" {
		t.Errorf("stored = %+v, want only the host entry", stored)
	}
	if err := Logout(t.Context(), "ghcr.io/my-lab/rnaseq", "user"); err == nil {
		t.Error("removing a missing credential succeeded")
	}
}
