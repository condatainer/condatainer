package cmd

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/condatainer/condatainer/internal/utils"
)

func TestConfirmDistroChange(t *testing.T) {
	old := utils.YesMode
	t.Cleanup(func() { utils.YesMode = old })
	ask := func(from, to string) error {
		return confirmDistroChange(context.Background(), io.Discard, from, to)
	}

	utils.YesMode = false
	if err := ask("", "ubuntu22"); err != nil {
		t.Errorf("a project with no base was asked: %v", err)
	}
	if err := ask("ubuntu22", "ubuntu22"); err != nil {
		t.Errorf("the same distro was asked: %v", err)
	}
	if err := ask("ubuntu24", "ubuntu22"); !errors.Is(err, errDistroChangeDeclined) {
		t.Errorf("a change was accepted with no answer: %v", err)
	}
	utils.YesMode = true
	if err := ask("ubuntu24", "ubuntu22"); err != nil {
		t.Errorf("--yes was refused: %v", err)
	}
}

func TestParseDistroArgs(t *testing.T) {
	for _, tc := range []struct {
		args             []string
		distro, identity string
		bad              bool
	}{
		{args: nil},
		{args: []string{"ubuntu22"}, distro: "ubuntu22"},
		{args: []string{"ubuntu22@b4f7d6e"}, distro: "ubuntu22", identity: "b4f7d6e"},
		{args: []string{"ubuntu22@payload-tree-v1@sha256:b4f7"}, distro: "ubuntu22", identity: "payload-tree-v1@sha256:b4f7"},
		{args: []string{"ubuntu22/base@b4f7d6e"}, distro: "ubuntu22", identity: "b4f7d6e"},
		{args: []string{"ubuntu22/base"}, distro: "ubuntu22"},
		{args: []string{"ubuntu22", "b4f7d6e"}, distro: "ubuntu22", identity: "b4f7d6e"},
		{args: []string{"ubuntu22@", "b4f7d6e"}, bad: true},
		{args: []string{"ubuntu22@b4f7d6e", "b4f7d6e"}, bad: true},
		{args: []string{"@b4f7d6e"}, bad: true},
	} {
		distro, identity, err := parseDistroArgs(tc.args)
		if (err != nil) != tc.bad || distro != tc.distro || identity != tc.identity {
			t.Errorf("%v: got %q, %q, %v", tc.args, distro, identity, err)
		}
	}
}
