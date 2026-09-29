package lan

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Pairing codes encode an IPv4 address (and a non-default port) in Crockford base32, fully offline:
// "192.168.1.23" becomes e.g. "60A-R0BQ". Same format as the Ferry Android app.
const pairAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func EncodePair(ip string, port int) string {
	v4 := net.ParseIP(ip).To4()
	if v4 == nil {
		return ""
	}
	v := uint64(v4[0])<<24 | uint64(v4[1])<<16 | uint64(v4[2])<<8 | uint64(v4[3])
	bits := 32
	if port != Port {
		v, bits = v<<16|uint64(port&0xFFFF), 48
	}
	n := (bits + 4) / 5
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		b[i] = pairAlphabet[(v>>(uint(n-1-i)*5))&31]
	}
	s := string(b)
	if len(s) == 7 {
		return s[:3] + "-" + s[3:]
	}
	return s[:5] + "-" + s[5:]
}

func DecodePair(code string) (ip string, port int, ok bool) {
	c := strings.NewReplacer("-", "", " ", "", "O", "0", "I", "1", "L", "1").Replace(strings.ToUpper(strings.TrimSpace(code)))
	if len(c) != 7 && len(c) != 10 {
		return "", 0, false
	}
	var v uint64
	for _, r := range c {
		d := strings.IndexRune(pairAlphabet, r)
		if d < 0 {
			return "", 0, false
		}
		v = v<<5 | uint64(d)
	}
	port = Port
	if len(c) == 10 {
		port, v = int(v&0xFFFF), v>>16
	}
	if v > 0xFFFFFFFF {
		return "", 0, false
	}
	return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)).String(), port, true
}

// QRLink is the "ferry://peer" link shown as a QR code for phones to scan (Ferry Android format).
func QRLink(addrs []string, port int, fingerprint, alias string) string {
	return "ferry://peer?h=" + url.QueryEscape(strings.Join(addrs, ",")) + "&p=" + strconv.Itoa(port) + "&f=" + fingerprint +
		"&n=" + url.QueryEscape(alias) + "&s=https"
}

// Target is where a user asked to connect: a pairing code, "ip[:port]", or a ferry://peer link.
type Target struct {
	Hosts       []string
	Port        int
	Fingerprint string
	HTTPS       *bool
	Source      string
}

func ParseTarget(in string) (Target, error) {
	in = strings.TrimSpace(in)
	if strings.HasPrefix(strings.ToLower(in), "ferry://peer") {
		u, err := url.Parse(in)
		if err != nil {
			return Target{}, err
		}
		q := u.Query()
		t := Target{Port: Port, Fingerprint: q.Get("f"), Source: "qr"}
		for _, h := range strings.Split(q.Get("h"), ",") {
			if h = strings.TrimSpace(h); h != "" {
				t.Hosts = append(t.Hosts, h)
			}
		}
		if p, err := strconv.Atoi(q.Get("p")); err == nil && p > 0 && p < 65536 {
			t.Port = p
		}
		if s := q.Get("s"); s == "http" || s == "https" {
			b := s == "https"
			t.HTTPS = &b
		}
		if len(t.Hosts) == 0 {
			return Target{}, errors.New("the link has no address")
		}
		return t, nil
	}
	if ip, port, ok := DecodePair(in); ok && !strings.Contains(in, ".") {
		return Target{Hosts: []string{ip}, Port: port, Source: "code"}, nil
	}
	host, port := in, Port
	if h, p, err := net.SplitHostPort(in); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return Target{}, errors.New("invalid port")
		}
		host, port = h, n
	}
	if net.ParseIP(host) == nil {
		if _, err := net.LookupHost(host); err != nil {
			return Target{}, errors.New("enter an IP address like 192.168.1.20, a pairing code like 60A-R0BQ, or a device name")
		}
	}
	return Target{Hosts: []string{host}, Port: port, Source: "manual"}, nil
}
