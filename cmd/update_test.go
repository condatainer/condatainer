package cmd

import (
	"errors"
	"testing"
)

func TestLibexecHostError(t *testing.T) {
	if err := libexecHostError(true); !errors.Is(err, errLibexecInContainer) {
		t.Errorf("libexecHostError(true) = %v, want errLibexecInContainer", err)
	}
	if err := libexecHostError(false); err != nil {
		t.Errorf("libexecHostError(false) = %v, want nil", err)
	}
}
