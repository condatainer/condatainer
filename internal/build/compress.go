package build

// CompressOption defines name, mksquashfs arguments, and description
type CompressOption struct {
	Name        string // e.g. "lz4" or "zstd-fast"
	Args        string // full mksquashfs arguments
	Description string // help text for CLI
}

// CompressOptions lists all supported compression shorthand names.
var CompressOptions = []CompressOption{
	{"gzip", "-comp gzip", "Use gzip compression"},
	{"lz4", "-comp lz4", "Use lz4 compression"},
	{"zstd", "-comp zstd -Xcompression-level 14", "Use zstd compression level 14"},
	{"zstd-fast", "-comp zstd -Xcompression-level 3", "Use zstd compression level 3"},
	{"zstd-medium", "-comp zstd -Xcompression-level 8", "Use zstd compression level 8"},
	{"zstd-high", "-comp zstd -Xcompression-level 19", "Use zstd compression level 19"},
}

// ArgsForCompress returns the full mksquashfs arguments corresponding to a
// recognised shortcut name. Return itself if no match found.
func ArgsForCompress(name string) string {
	for _, o := range CompressOptions {
		if o.Name == name {
			return o.Args
		}
	}
	return name
}

// CompressNames returns a list of the shortcut names (used for completion)
func CompressNames() []string {
	names := make([]string, len(CompressOptions))
	for i, o := range CompressOptions {
		names[i] = o.Name
	}
	return names
}
