package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAppPathsPrecedenceAndShape(t *testing.T) {
	t.Parallel()
	explicit := filepath.Join(t.TempDir(), "explicit")
	paths, err := resolveAppPaths(pathEnvironment{
		GOOS:         "linux",
		ExplicitHome: explicit,
		ICloudHome:   filepath.Join(t.TempDir(), "environment"),
		XDGDataHome:  filepath.Join(t.TempDir(), "xdg"),
		UserHome:     func() (string, error) { return t.TempDir(), nil },
		UserConfig:   func() (string, error) { return t.TempDir(), nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	absolute, _ := filepath.Abs(explicit)
	if paths.Root != absolute {
		t.Fatalf("root = %q, want %q", paths.Root, absolute)
	}
	if paths.Database != filepath.Join(absolute, "mail.db") ||
		paths.BlobRoot != filepath.Join(absolute, "blobs") ||
		paths.LockRoot != filepath.Join(absolute, "locks") ||
		paths.MigrationLock != filepath.Join(absolute, "locks", "migrate.lock") {
		t.Fatalf("unexpected paths: %+v", paths)
	}
}

func TestResolveAppPathsUsesEnvironmentAndPlatformDefaults(t *testing.T) {
	t.Parallel()
	home := filepath.Join(t.TempDir(), "home")
	xdg := filepath.Join(t.TempDir(), "xdg")
	icloudHome := filepath.Join(t.TempDir(), "icloud-home")
	windowsConfig := filepath.Join(t.TempDir(), "AppData", "Roaming")

	tests := []struct {
		name        string
		environment pathEnvironment
		want        string
	}{
		{
			name: "icloud home",
			environment: pathEnvironment{
				GOOS:       "linux",
				ICloudHome: icloudHome,
				UserHome:   func() (string, error) { return home, nil },
				UserConfig: func() (string, error) { return home, nil },
			},
			want: icloudHome,
		},
		{
			name: "linux xdg",
			environment: pathEnvironment{
				GOOS:        "linux",
				XDGDataHome: xdg,
				UserHome:    func() (string, error) { return home, nil },
				UserConfig:  func() (string, error) { return home, nil },
			},
			want: filepath.Join(xdg, appDirectoryName),
		},
		{
			name: "linux fallback",
			environment: pathEnvironment{
				GOOS:       "freebsd",
				UserHome:   func() (string, error) { return home, nil },
				UserConfig: func() (string, error) { return home, nil },
			},
			want: filepath.Join(home, ".local", "share", appDirectoryName),
		},
		{
			name: "macos",
			environment: pathEnvironment{
				GOOS:       "darwin",
				UserHome:   func() (string, error) { return home, nil },
				UserConfig: func() (string, error) { return home, nil },
			},
			want: filepath.Join(home, "Library", "Application Support", appDirectoryName),
		},
		{
			name: "windows",
			environment: pathEnvironment{
				GOOS:       "windows",
				UserHome:   func() (string, error) { return home, nil },
				UserConfig: func() (string, error) { return windowsConfig, nil },
			},
			want: filepath.Join(windowsConfig, appDirectoryName),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			paths, err := resolveAppPaths(test.environment)
			if err != nil {
				t.Fatal(err)
			}
			absolute, _ := filepath.Abs(test.want)
			if paths.Root != absolute {
				t.Fatalf("root = %q, want %q", paths.Root, absolute)
			}
		})
	}
}

func TestResolveAppPathsReportsMissingHome(t *testing.T) {
	t.Parallel()
	_, err := resolveAppPaths(pathEnvironment{
		GOOS:       "linux",
		UserHome:   func() (string, error) { return "", errors.New("missing") },
		UserConfig: func() (string, error) { return "", nil },
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEnsureAppPathsCreatesPrivateDirectories(t *testing.T) {
	t.Parallel()
	paths, err := resolveAppPaths(pathEnvironment{
		GOOS:         "linux",
		ExplicitHome: filepath.Join(t.TempDir(), "state"),
		UserHome:     os.UserHomeDir,
		UserConfig:   os.UserConfigDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := EnsureAppPaths(paths); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Root, paths.BlobRoot, paths.LockRoot} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", path)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Fatalf("mode for %s = %o", path, got)
		}
	}
}
