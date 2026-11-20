package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smtp.env")
	content := "# comment\n\nMT_FROM=折光观察者 <noreply@example.com>\nexport MT_PORT = 465\nMT_QUOTED=\"a b\"\nMT_KEPT=file\nMT_EQ=x=y\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MT_KEPT", "env")
	for _, k := range []string{"MT_FROM", "MT_PORT", "MT_QUOTED", "MT_EQ"} {
		t.Setenv(k, "") // registers cleanup
		os.Unsetenv(k)
	}
	if err := loadEnvFile(path); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"MT_FROM": "折光观察者 <noreply@example.com>", "MT_PORT": "465", "MT_QUOTED": "a b", "MT_KEPT": "env", "MT_EQ": "x=y",
	} {
		if got := os.Getenv(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}

	bad := filepath.Join(t.TempDir(), "bad.env")
	os.WriteFile(bad, []byte("NOEQUALS\n"), 0o600)
	if err := loadEnvFile(bad); err == nil {
		t.Error("malformed line accepted")
	}
}
