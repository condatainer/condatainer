package sif

import (
	"fmt"

	"github.com/condatainer/condatainer/internal/image/squashfs"
	"github.com/condatainer/condatainer/internal/utils"
)

// ReadFile reads one file out of a SIF's SquashFS partition without extracting
// the partition: the partition offset found in the descriptor table is handed
// to unsquashfs -offset, which reads the archive in place.
//
// Errors keep their cause, so a caller can tell a missing file from a missing
// tool from a corrupt image.
func ReadFile(path, innerPath string) ([]byte, error) {
	part, err := PrimarySystemPartition(path)
	if err != nil {
		return nil, err
	}
	return squashfs.CatFile(path, innerPath, part.Offset)
}

// RequireBash checks that a SIF's own root has /bin/bash.
func RequireBash(path string) error {
	if _, err := ReadFile(path, utils.BashPath); err != nil {
		return fmt.Errorf("this base provides no /%s; every build runs inside the base and needs it: %w", utils.BashPath, err)
	}
	return nil
}
