// Package statedir resolves the conclave state directory and the daemon token
// that every local client needs in order to reach the daemon API.
package statedir

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const tokenLength = 64

// Default returns the state directory used when no explicit path is given.
func Default() string {
	if directory := os.Getenv("LOCALAPPDATA"); directory != "" {
		return filepath.Join(directory, "conclave")
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return ".conclave"
	}
	return filepath.Join(directory, "conclave")
}

// TokenPath returns the token file inside the default state directory.
func TokenPath() string { return filepath.Join(Default(), "token") }

// ReadToken loads a daemon token and rejects anything that is not a full token.
func ReadToken(path string) (string, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(value))
	if len(token) != tokenLength {
		return "", errors.New("daemon token is invalid")
	}
	return token, nil
}

// LoadOrCreateToken returns the existing token or generates one exclusively, so
// two processes racing on a fresh state directory still agree on a single token.
func LoadOrCreateToken(path string) (string, error) {
	token, err := ReadToken(path)
	if err == nil {
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	random := make([]byte, tokenLength/2)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	token = hex.EncodeToString(random)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ReadToken(path)
		}
		return "", err
	}
	if _, err := file.WriteString(token); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return token, nil
}
