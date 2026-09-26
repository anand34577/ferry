package db

import "testing"

func TestRebind(t *testing.T) {
	pg := &DB{Dialect: "postgres"}
	if got := pg.rebind("SELECT a FROM t WHERE x = ? AND y IN (?, ?)"); got != "SELECT a FROM t WHERE x = $1 AND y IN ($2, $3)" {
		t.Fatal(got)
	}
	if got := (&DB{Dialect: "sqlite"}).rebind("x = ?"); got != "x = ?" {
		t.Fatal(got)
	}
}
