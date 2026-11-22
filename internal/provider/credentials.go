// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// cliConfig is the part of the LiveKit CLI's configuration
// (~/.livekit/cli-config.yaml, written by `lk cloud auth`) that names the
// signed-in users and their sessions. Layout as of lk 2.20.1 (2026-11-22).
type cliConfig struct {
	DefaultUser string `yaml:"default_user"`
	Users       []struct {
		ID            string `yaml:"id"`
		Email         string `yaml:"email"`
		SessionToken  string `yaml:"session_token"`
		SessionExpiry int64  `yaml:"session_expiry"`
		APIURL        string `yaml:"api_url"`
	} `yaml:"users"`
}

// cliSession is a session found in the LiveKit CLI's configuration.
type cliSession struct {
	token   string
	expiry  time.Time // zero when unknown
	apiURL  string
	user    string
	cfgPath string
}

// defaultCLIConfigPath is where lk keeps its configuration.
func defaultCLIConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".livekit", "cli-config.yaml")
}

// errNoCLIConfig means there is no lk configuration file to read.
var errNoCLIConfig = errors.New("no LiveKit CLI configuration")

// readCLISession returns the session of the CLI's default user, or of the
// user whose ID or email is `user` when that is set.
func readCLISession(path, user string) (*cliSession, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, errNoCLIConfig
	}
	if err != nil {
		return nil, err
	}
	var cfg cliConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	want := user
	if want == "" {
		want = cfg.DefaultUser
	}
	if want == "" && len(cfg.Users) == 1 {
		want = cfg.Users[0].ID
	}
	for _, u := range cfg.Users {
		if want == "" || (u.ID != want && u.Email != want) {
			continue
		}
		if u.SessionToken == "" {
			return nil, fmt.Errorf("%s has no session for %s: sign in with `lk cloud auth`", path, u.Email)
		}
		s := &cliSession{token: u.SessionToken, apiURL: u.APIURL, user: u.Email, cfgPath: path}
		if u.SessionExpiry > 0 {
			s.expiry = time.Unix(u.SessionExpiry, 0)
		}
		return s, nil
	}
	if user != "" {
		return nil, fmt.Errorf("%s has no signed-in user %q", path, user)
	}
	return nil, fmt.Errorf("%s has no signed-in user: sign in with `lk cloud auth`", path)
}
