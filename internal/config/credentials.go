package config

import (
	"path/filepath"

	"github.com/condatainer/condatainer/internal/credential"
)

// credentialLayers are the layers that can hold credentials, nearest first.
var credentialLayers = []string{"user", "extra-root", "app-root"}

// CredentialFile is a config layer's credential file, beside its config file,
// whether or not it exists yet.
func CredentialFile(layer string) (credential.File, error) {
	layer = NormalizeConfigLayer(layer)
	path, err := GetConfigPathByLayer(layer)
	if err != nil {
		return credential.File{}, err
	}
	return credential.File{Layer: layer, Path: filepath.Join(filepath.Dir(path), credential.FileName)}, nil
}

// CredentialFiles lists every available layer's credential file, nearest first.
func CredentialFiles() []credential.File {
	var out []credential.File
	for _, layer := range credentialLayers {
		if f, err := CredentialFile(layer); err == nil {
			out = append(out, f)
		}
	}
	return out
}
