module github.com/condatainer/condatainer

go 1.26.4

replace golang.org/x/crypto => github.com/condatainer/x-crypto v0.57.0-hostbased.1

require (
	github.com/chzyer/readline v1.5.1
	github.com/fatih/color v1.19.0
	github.com/opencontainers/go-digest v1.0.0
	github.com/opencontainers/image-spec v1.1.1
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.10
	go.yaml.in/yaml/v3 v3.0.5
	golang.org/x/crypto v0.57.0-hostbased.1
	golang.org/x/mod v0.41.0
	golang.org/x/sync v0.23.0
	golang.org/x/sys v0.48.0
	golang.org/x/term v0.46.0
	oras.land/oras-go/v2 v2.6.2
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-colorable v0.1.15 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
)
