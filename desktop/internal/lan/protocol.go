// Package lan implements direct transfers on the local network with the LocalSend v2 protocol
// (https://github.com/localsend/protocol), plus Ferry's extensions for resuming and verifying files.
// It interoperates with LocalSend on every platform and with the Ferry Android app.
package lan

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	MulticastGroup = "224.0.0.167"
	Port           = 53317
	Version        = "2.1"
	API            = "/api/localsend/v2"
)

// Info is a device's LocalSend identity (discovery, /info, /register and prepare-upload "info").
type Info struct {
	Alias        string `json:"alias"`
	Version      string `json:"version"`
	DeviceModel  string `json:"deviceModel,omitempty"`
	DeviceType   string `json:"deviceType,omitempty"`
	Fingerprint  string `json:"fingerprint"`
	Port         int    `json:"port"`
	Protocol     string `json:"protocol"`
	Download     bool   `json:"download"`
	Ferry        int    `json:"ferry,omitempty"` // extension marker: resume + verify supported; ignored by stock LocalSend
	Announce     *bool  `json:"announce,omitempty"`
	Announcement *bool  `json:"announcement,omitempty"` // LocalSend v1 name of the same flag
}

// SelfInfo is what this device announces. It carries only what's needed to connect — no account data.
func SelfInfo(alias, fingerprint string, port int, announce *bool) Info {
	return Info{Alias: alias, Version: Version, DeviceModel: "Windows", DeviceType: "desktop", Fingerprint: fingerprint,
		Port: port, Protocol: "https", Ferry: 1, Announce: announce, Announcement: announce}
}

// Peer is a device found on the network (or entered by hand).
type Peer struct {
	IP              string `json:"ip"`
	Port            int    `json:"port"`
	HTTPS           bool   `json:"https"`
	Alias           string `json:"alias"`
	Model           string `json:"model"`
	Type            string `json:"type"`
	Fingerprint     string `json:"fingerprint"` // self-asserted identifier
	CertPin         string `json:"certPin"`     // SHA-256 of the certificate actually seen; enforced on later connections
	Source          string `json:"source"`      // multicast | scan | manual | code | qr | server
	Ferry           bool   `json:"ferry"`
	AccountDeviceID string `json:"accountDeviceId,omitempty"`
	LastSeen        int64  `json:"lastSeen"`
}

func (p Peer) Key() string {
	if p.Fingerprint != "" {
		return p.Fingerprint
	}
	return p.IP + ":" + strconv.Itoa(p.Port)
}

func (p Peer) BaseURL() string {
	scheme := "http"
	if p.HTTPS {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(p.IP, strconv.Itoa(p.Port))
}

func peerFrom(i Info, ip, source string, fallbackPort int, fallbackHTTPS bool) Peer {
	p := Peer{IP: ip, Port: i.Port, Alias: clip(strings.TrimSpace(i.Alias), 60), Model: clip(i.DeviceModel, 60), Type: i.DeviceType,
		Fingerprint: i.Fingerprint, Source: source, Ferry: i.Ferry >= 1}
	if p.Port <= 0 || p.Port > 65535 {
		p.Port = fallbackPort
	}
	switch i.Protocol {
	case "https":
		p.HTTPS = true
	case "http":
	default:
		p.HTTPS = fallbackHTTPS
	}
	if p.Alias == "" {
		p.Alias = "Unknown device"
	}
	if p.Type == "" {
		p.Type = "mobile"
	}
	return p
}

// FileMeta is one entry of a prepare-upload request.
type FileMeta struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`
	FileType string `json:"fileType"`
	SHA256   string `json:"sha256,omitempty"`
}

type prepareReq struct {
	Info  Info                `json:"info"`
	Files map[string]FileMeta `json:"files"`
}

type prepareResp struct {
	SessionID string            `json:"sessionId"`
	Files     map[string]string `json:"files"`
}

// PeerError is an HTTP error answered by a peer.
type PeerError struct {
	Status  int
	Message string
}

func (e *PeerError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
