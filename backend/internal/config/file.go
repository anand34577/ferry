package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FilePath returns the configuration file to use: explicit (flag or FERRY_CONFIG), else ferry.env next to
// the executable, else ferry.env in the working directory. Empty when none exists.
func FilePath(explicit string) string {
	if explicit == "" {
		explicit = os.Getenv("FERRY_CONFIG")
	}
	if explicit != "" {
		return explicit
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		if exe, err = filepath.EvalSymlinks(exe); err == nil {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), "ferry.env"))
		}
	}
	candidates = append(candidates, "ferry.env")
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// LoadFile applies KEY=VALUE lines from path to the environment. Variables that are already set win, so
// the environment (e.g. Docker) can always override the file. Blank lines and # comments are ignored;
// values may be wrapped in single or double quotes.
func LoadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" || strings.ContainsAny(k, " \t") {
			return fmt.Errorf("%s line %d: expected KEY=VALUE", path, n)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 { // trailing comment on an unquoted value
			v = strings.TrimSpace(v[:i])
		}
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
	return sc.Err()
}
