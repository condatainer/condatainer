package container

// hostEnvUnset names host variables removed before launch.
//   - Each names a host install or path.
//   - It breaks a tool, or changes where packages are downloaded, installed or found.
//   - A user can still set any of them with --env.
var hostEnvUnset = []string{
	// TLS certificate files
	"CURL_CA_BUNDLE", "SSL_CERT_FILE", "SSL_CERT_DIR", "REQUESTS_CA_BUNDLE",
	"GIT_SSL_CAINFO", "PIP_CERT", "NODE_EXTRA_CA_CERTS", "AWS_CA_BUNDLE",

	// R
	"R_HOME", "R_LIBS", "R_LIBS_USER", "R_LIBS_SITE",

	// Python
	"PYTHONPATH", "PYTHONHOME", "PYTHONUSERBASE",

	// Perl
	"PERL5LIB", "PERL_LOCAL_LIB_ROOT", "PERL_MB_OPT", "PERL_MM_OPT",

	// Java
	"JAVA_HOME",

	// Julia
	"JULIA_DEPOT_PATH",

	// Ruby
	"GEM_HOME", "GEM_PATH",

	// Node
	"NODE_PATH", "NPM_CONFIG_PREFIX",

	// Rust
	"CARGO_HOME", "RUSTUP_HOME",

	// Go
	"GOROOT",

	// Compile and link paths
	"CPATH", "C_INCLUDE_PATH", "CPLUS_INCLUDE_PATH",
	"LIBRARY_PATH", "PKG_CONFIG_PATH", "CMAKE_PREFIX_PATH",

	// Conda
	"CONDA_PREFIX", "CONDARC", "MAMBA_ROOT_PREFIX",
	"CONDA_PKGS_DIRS", "CONDA_ENVS_PATH", "CONDA_ENVS_DIRS",

	// Dynamic loader
	"LD_PRELOAD",
}
