package service

import (
	"strings"
	"testing"
)

func TestURLs(t *testing.T) {
	if got := URLs("127.0.0.1:9000"); len(got) != 1 || got[0] != "http://127.0.0.1:9000" {
		t.Fatal(got)
	}
	if got := URLs(":8080"); len(got) == 0 || got[0] != "http://localhost:8080" {
		t.Fatal(got)
	}
	for _, u := range URLs(":8080") {
		if !strings.HasPrefix(u, "http://") || !strings.HasSuffix(u, ":8080") {
			t.Fatal(u)
		}
	}
}
