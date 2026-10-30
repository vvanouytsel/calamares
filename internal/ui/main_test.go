package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the config at a scratch file, so no test can change the
// config of whoever runs the tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "calamares-test")
	if err != nil {
		panic(err)
	}
	os.Setenv("CALAMARES_CONFIG", filepath.Join(dir, "config.yaml"))
	os.Setenv("CALAMARES_ANIMATIONS", "off")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
