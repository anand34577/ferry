package service

import (
	"strings"
	"testing"
)

func TestUnit(t *testing.T) {
	u := unit(Default())
	for _, want := range []string{
		"User=ferry\n", "Environment=FERRY_CONFIG=/etc/ferry/ferry.env\n", "ExecStart=/usr/local/bin/ferry serve\n",
		"ReadWritePaths=/var/lib/ferry\n", "ProtectSystem=strict\n", "WantedBy=multi-user.target\n",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("unit is missing %q", want)
		}
	}
}
