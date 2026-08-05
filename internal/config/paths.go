package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const appDirectoryName = "icloud-cli"

// AppPaths contains every durable path owned by the standalone CLI.
type AppPaths struct {
	Root          string
	Database      string
	BlobRoot      string
	LockRoot      string
	MigrationLock string
}

// ResolveAppPaths applies explicit --home, ICLOUD_HOME, and platform defaults in
// that order. Returned paths are absolute and do not create filesystem state.
func ResolveAppPaths(explicitHome string) (AppPaths, error) {
	return resolveAppPaths(pathEnvironment{
		GOOS:         runtime.GOOS,
		ExplicitHome: explicitHome,
		ICloudHome:   os.Getenv("ICLOUD_HOME"),
		XDGDataHome:  os.Getenv("XDG_DATA_HOME"),
		UserHome:     userHomeDir,
		UserConfig:   userConfigDir,
	})
}

type pathEnvironment struct {
	GOOS         string
	ExplicitHome string
	ICloudHome   string
	XDGDataHome  string
	UserHome     func() (string, error)
	UserConfig   func() (string, error)
}

func resolveAppPaths(environment pathEnvironment) (AppPaths, error) {
	root := strings.TrimSpace(environment.ExplicitHome)
	if root == "" {
		root = strings.TrimSpace(environment.ICloudHome)
	}
	if root == "" {
		var err error
		root, err = defaultDataRoot(environment)
		if err != nil {
			return AppPaths{}, err
		}
	}

	absoluteRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return AppPaths{}, fmt.Errorf("resolve application data directory: %w", err)
	}
	return AppPaths{
		Root:          absoluteRoot,
		Database:      filepath.Join(absoluteRoot, "mail.db"),
		BlobRoot:      filepath.Join(absoluteRoot, "blobs"),
		LockRoot:      filepath.Join(absoluteRoot, "locks"),
		MigrationLock: filepath.Join(absoluteRoot, "locks", "migrate.lock"),
	}, nil
}

func defaultDataRoot(environment pathEnvironment) (string, error) {
	switch environment.GOOS {
	case "darwin":
		home, err := environment.UserHome()
		if err != nil {
			return "", fmt.Errorf("resolve user home directory: %w", err)
		}
		return filepath.Join(home, "Library", "Application Support", appDirectoryName), nil
	case "windows":
		root, err := environment.UserConfig()
		if err != nil {
			return "", fmt.Errorf("resolve user application data directory: %w", err)
		}
		return filepath.Join(root, appDirectoryName), nil
	default:
		if root := strings.TrimSpace(environment.XDGDataHome); root != "" {
			return filepath.Join(root, appDirectoryName), nil
		}
		home, err := environment.UserHome()
		if err != nil {
			return "", fmt.Errorf("resolve user home directory: %w", err)
		}
		return filepath.Join(home, ".local", "share", appDirectoryName), nil
	}
}

// EnsureAppPaths creates the private application, blob, and lock directories.
func EnsureAppPaths(paths AppPaths) error {
	for _, path := range []string{paths.Root, paths.BlobRoot, paths.LockRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create application data directory: %w", err)
		}
		if err := os.Chmod(path, 0o700); err != nil {
			return fmt.Errorf("secure application data directory: %w", err)
		}
	}
	return nil
}

var (
	userHomeDir   = os.UserHomeDir
	userConfigDir = os.UserConfigDir
)
