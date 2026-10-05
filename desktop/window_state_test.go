//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowStateRejectsInvalidOrOversizedGeometry(t *testing.T) {
	root := t.TempDir()
	for _, value := range []string{`{`, `{"width":-100,"height":600}`, `{"width":999999,"height":700}`, `{"width":800,"height":1}`} {
		if err := os.WriteFile(filepath.Join(root, "window.json"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if got := loadWindowState(root); got.Width != 960 || got.Height != 680 || got.Maximised {
			t.Fatal(got)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "window.json"), []byte(`{"width":1100,"height":720,"maximised":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := loadWindowState(root); got.Width != 1100 || got.Height != 720 || !got.Maximised {
		t.Fatal(got)
	}
}

func TestNativeThemeRejectsInvalidValueWithoutWindow(t *testing.T) {
	a := &App{}
	if a.SetTheme("invalid") == nil || a.SetTheme("dark") == nil {
		t.Fatal("invalid or uninitialised theme accepted")
	}
}
