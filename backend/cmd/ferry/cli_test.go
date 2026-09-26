package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestExtractConfigFlag(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		rest []string
		file string
	}{
		{[]string{"serve", "--config", "a.env"}, []string{"serve"}, "a.env"},
		{[]string{"--config=b.env", "user", "list"}, []string{"user", "list"}, "b.env"},
		{[]string{"-config", "c.env"}, nil, "c.env"},
		{[]string{"version"}, []string{"version"}, ""},
	} {
		rest, file := extractConfigFlag(tc.in)
		if !reflect.DeepEqual(rest, tc.rest) || file != tc.file {
			t.Errorf("%v → %v %q", tc.in, rest, file)
		}
	}
}

func TestEnvForService(t *testing.T) {
	s := envForService(envExample, "/var/lib/ferry", "/var/log/ferry.log", ":9000")
	for _, want := range []string{"\nFERRY_DATA_DIR=/var/lib/ferry\n", "\nFERRY_LOG_FILE=/var/log/ferry.log\n", "\nFERRY_ADDR=:9000\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(envForService(envExample, "/d", "", ":8080"), "\nFERRY_ADDR=") {
		t.Error("default address should stay commented out")
	}
	// A user's own file keeps their settings; missing service paths are added.
	got := envForService("FERRY_ADDR=:9999\nFERRY_SITE_NAME=Home\n", "/var/lib/ferry", "", ":8080")
	if got != "FERRY_ADDR=:9999\nFERRY_SITE_NAME=Home\nFERRY_DATA_DIR=/var/lib/ferry\n" {
		t.Errorf("%q", got)
	}
}

// The shipped example must be the same file the service installer writes.
func TestEnvExampleInSync(t *testing.T) {
	b, err := os.ReadFile("../../../deployment/.env.example")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(b), "\r\n", "\n") != envExample {
		t.Fatal("deployment/.env.example differs from envExample; regenerate it with: ferry config example > deployment/.env.example")
	}
}
