package container

import (
	"github.com/condatainer/condatainer/internal/conda"
	"github.com/condatainer/condatainer/internal/utils"
)

// PairedPackages returns every package installed at path, a .img or a bare .sqf, merged with an autoloaded snapshot's packages (LookupSnapshot) when path is a .img that pairs with one.
//   - The snapshot goes first, so path's own entries win on conflict.
//   - Callers use this instead of conda.ListCondaPackages directly.
//   - It returns (nil, nil) only when neither side has a conda environment.
func PairedPackages(path string) (map[string]string, error) {
	if utils.IsSqf(path) {
		return conda.ListCondaPackagesSqf(path)
	}
	imgPkgs, err := conda.ListCondaPackages(path)
	if err != nil {
		return nil, err
	}
	var snapshotPkgs map[string]string
	if utils.IsImg(path) {
		if lookup := LookupSnapshot(path); lookup.Path != "" {
			if snapshotPkgs, err = conda.ListCondaPackagesSqf(lookup.Path); err != nil {
				return nil, err
			}
		}
	}
	if snapshotPkgs == nil && imgPkgs == nil {
		return nil, nil
	}
	merged := make(map[string]string, len(snapshotPkgs)+len(imgPkgs))
	for name, ver := range snapshotPkgs {
		merged[name] = ver
	}
	for name, ver := range imgPkgs {
		merged[name] = ver
	}
	return merged, nil
}

// PairedInfo reads channels and explicitly installed specs for path, merging a .img's conda-meta/history with an autoloaded snapshot's as one continuous log when path pairs with one (LookupSnapshot).
//   - A bare .sqf or an unpaired .img is read alone.
//   - Display code uses this instead of conda.ReadCondaInfo directly.
func PairedInfo(path, envPrefix string) *conda.CondaInfo {
	if utils.IsImg(path) {
		if lookup := LookupSnapshot(path); lookup.Path != "" {
			return conda.ReadCondaInfoMerged(lookup.Path, path, envPrefix)
		}
	}
	return conda.ReadCondaInfo(path, envPrefix)
}
