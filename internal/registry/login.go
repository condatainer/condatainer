package registry

import (
	"context"
	"fmt"
	"strings"

	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"

	"github.com/condatainer/condatainer/internal/credential"
)

// Login verifies a credential against the registry host in target and stores it in one config layer, under target itself: a bare host, or host/owner/repo to cover one repository.
//   - The layer is "user", "extra-root" or "app-root".
//   - The layer's directory must already exist unless it is the user's.
func Login(ctx context.Context, target, layer, username, password string) error {
	key, err := splitTarget(target)
	if err != nil {
		return err
	}
	file, err := credentialFile(layer)
	if err != nil {
		return err
	}
	if err := CheckLogin(ctx, key, username, password); err != nil {
		return err
	}
	return credential.Save(file, credential.Registry, key, credential.Credential{Username: username, Secret: password})
}

// CheckLogin verifies a credential against the registry host in target, saving
// nothing.
func CheckLogin(ctx context.Context, target, username, password string) error {
	key, err := splitTarget(target)
	if err != nil {
		return err
	}
	if password == "" {
		return fmt.Errorf("registry password or token is empty")
	}
	host, _, _ := strings.Cut(key, "/")
	reg, err := remote.NewRegistry(host)
	if err != nil {
		return fmt.Errorf("invalid registry %q: %w", host, err)
	}
	reg.Client = &auth.Client{
		Client:     retry.DefaultClient,
		Cache:      auth.NewCache(),
		Credential: auth.StaticCredential(reg.Reference.Registry, auth.Credential{Username: username, Password: password}),
	}
	reg.PlainHTTP = isLoopback(reg.Reference.Registry)
	if err := reg.Ping(ctx); err != nil {
		return fmt.Errorf("login to %s failed: %w", key, classify(err))
	}
	return nil
}

// Logout removes the credential stored under target in one config layer.
func Logout(_ context.Context, target, layer string) error {
	key, err := splitTarget(target)
	if err != nil {
		return err
	}
	file, err := credentialFile(layer)
	if err != nil {
		return err
	}
	return credential.Remove(file, credential.Registry, key)
}

// splitTarget turns a login target into its key: a bare host, or a host with a
// repository path, nothing else.
func splitTarget(target string) (key string, err error) {
	key = TrimBaseScheme(target)
	host, _, _ := strings.Cut(key, "/")
	if host == "" || strings.Contains(key, "//") || strings.ContainsAny(key, " \t?#@") {
		return "", fmt.Errorf("%q is not a registry host or host/owner/repository", target)
	}
	return key, nil
}
