package config

import (
	"errors"
	"path/filepath"
	"runtime"

	"github.com/s8a-uk/modrinth-cli/internal/apperr"
)

// Paths are the locations of the config files.
type Paths struct {
	Dir         string
	ConfigFile  string // Dir/config.json
	Credentials string // Dir/credentials.json
}

// ResolvePaths returns MODRINTH_CONFIG_DIR, else <user config dir>/modrinth-cli.
// The user config dir follows os.UserConfigDir, but reads the injected env so
// tests never see the host's environment.
func ResolvePaths(env Env) (Paths, error) {
	dir := env("MODRINTH_CONFIG_DIR")
	if dir == "" {
		base, err := userConfigDir(env, runtime.GOOS)
		if err != nil {
			return Paths{}, err
		}
		dir = filepath.Join(base, "modrinth-cli")
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return Paths{
		Dir:         dir,
		ConfigFile:  filepath.Join(dir, "config.json"),
		Credentials: filepath.Join(dir, "credentials.json"),
	}, nil
}

// userConfigDir mirrors os.UserConfigDir: %AppData% on Windows,
// $HOME/Library/Application Support on macOS, else $XDG_CONFIG_HOME (if absolute)
// or $HOME/.config.
func userConfigDir(env Env, goos string) (string, error) {
	switch goos {
	case "windows":
		if d := env("APPDATA"); d != "" {
			return d, nil
		}
		return "", errors.New("%AppData% is not defined")
	case "darwin", "ios":
		if h := env("HOME"); h != "" {
			return h + "/Library/Application Support", nil
		}
		return "", errors.New("$HOME is not defined")
	}
	if d := env("XDG_CONFIG_HOME"); d != "" && filepath.IsAbs(d) {
		return d, nil
	}
	if h := env("HOME"); h != "" {
		return filepath.Join(h, ".config"), nil
	}
	return "", errors.New("neither $XDG_CONFIG_HOME nor $HOME is defined")
}

// RequireDir is the error of commands that write files when there is no config directory.
func RequireDir(p Paths) error {
	if p.Dir == "" {
		return apperr.New(apperr.IOError, "cannot determine the config directory").
			WithHint("set MODRINTH_CONFIG_DIR")
	}
	return nil
}
