package config

import (
	"os"
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{"1024": 1024, "500MB": 500 << 20, "10 GiB": 10 << 30, "1.5k": 1536, "2t": 2 << 40, "0": 0} {
		if got, err := ParseSize(in); err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "-1MB", "10XB"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) should fail", bad)
		}
	}
}

func TestDurEnv(t *testing.T) {
	t.Setenv("FERRY_T", "7d")
	if d, err := durEnv("FERRY_T", 0); err != nil || d != 7*24*time.Hour {
		t.Fatal(d, err)
	}
	t.Setenv("FERRY_T", "90m")
	if d, err := durEnv("FERRY_T", 0); err != nil || d != 90*time.Minute {
		t.Fatal(d, err)
	}
	t.Setenv("FERRY_T", "soon")
	if _, err := durEnv("FERRY_T", 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadValidation(t *testing.T) {
	t.Setenv("FERRY_DB_DRIVER", "postgres")
	t.Setenv("FERRY_TLS_CERT", "cert.pem")
	if _, err := Load(); err == nil {
		t.Fatal("postgres without DSN and cert without key must fail")
	}
}

func TestLoadFile(t *testing.T) {
	p := t.TempDir() + "/ferry.env"
	os.WriteFile(p, []byte("# comment\n\nFERRY_T_A=plain # note\nexport FERRY_T_B=\"quoted # kept\"\nFERRY_T_C=from-file\n"), 0o600)
	t.Setenv("FERRY_T_C", "from-env")
	t.Setenv("FERRY_T_A", "")
	os.Unsetenv("FERRY_T_A")
	t.Cleanup(func() { os.Unsetenv("FERRY_T_A"); os.Unsetenv("FERRY_T_B") })
	if err := LoadFile(p); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FERRY_T_A") != "plain" || os.Getenv("FERRY_T_B") != "quoted # kept" || os.Getenv("FERRY_T_C") != "from-env" {
		t.Fatal(os.Getenv("FERRY_T_A"), os.Getenv("FERRY_T_B"), os.Getenv("FERRY_T_C"))
	}
	os.WriteFile(p, []byte("not a setting\n"), 0o600)
	if err := LoadFile(p); err == nil {
		t.Fatal("expected a syntax error")
	}
}
