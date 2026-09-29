package lan

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/ipv4"
)

// Discovery finds nearby devices: LocalSend multicast announcements, answers to our own announcements,
// a subnet scan when multicast is blocked, and addresses entered by hand (IP, pairing code or QR).
type Discovery struct {
	id        *Identity
	self      func() Info
	receiving func() bool
	log       *slog.Logger
	onChange  func()

	mu       sync.Mutex
	peers    map[string]Peer
	conn     *ipv4.PacketConn
	fast     atomic.Bool
	scanning atomic.Bool
	stop     chan struct{}
	wake     chan struct{}
}

func NewDiscovery(id *Identity, self func() Info, receiving func() bool, onChange func(), log *slog.Logger) *Discovery {
	return &Discovery{id: id, self: self, receiving: receiving, onChange: onChange, log: log, peers: map[string]Peer{},
		stop: make(chan struct{}), wake: make(chan struct{}, 1)}
}

// Start listens for announcements and announces this device periodically until Stop.
func (d *Discovery) Start() {
	go d.listen()
	go func() {
		for {
			d.Announce()
			d.prune()
			wait := 30 * time.Second
			if d.fast.Load() {
				wait = 4 * time.Second
			}
			select {
			case <-d.stop:
				return
			case <-d.wake:
			case <-time.After(wait):
			}
		}
	}()
}

func (d *Discovery) Stop() {
	close(d.stop)
	d.mu.Lock()
	if d.conn != nil {
		d.conn.Close()
	}
	d.mu.Unlock()
}

// SetFast announces every few seconds while the user is looking for devices.
func (d *Discovery) SetFast(on bool) {
	if !d.fast.Swap(on) && on {
		select {
		case d.wake <- struct{}{}:
		default:
		}
	}
}

func (d *Discovery) Scanning() bool { return d.scanning.Load() }

func (d *Discovery) listen() {
	group := net.ParseIP(MulticastGroup)
	for {
		select {
		case <-d.stop:
			return
		default:
		}
		lc := net.ListenConfig{Control: reuseAddr}
		c, err := lc.ListenPacket(context.Background(), "udp4", ":"+strconv.Itoa(Port))
		if err != nil {
			d.log.Warn("multicast listener", "err", err)
			if !d.sleep(5 * time.Second) {
				return
			}
			continue
		}
		pc := ipv4.NewPacketConn(c)
		joined := 0
		for _, ifi := range lanInterfaces() {
			if pc.JoinGroup(&ifi, &net.UDPAddr{IP: group}) == nil {
				joined++
			}
		}
		if joined == 0 {
			pc.JoinGroup(nil, &net.UDPAddr{IP: group})
		}
		d.mu.Lock()
		d.conn = pc
		d.mu.Unlock()
		buf := make([]byte, 8192)
		// Rejoin periodically: Wi-Fi changes, VPNs and sleep/resume replace the interfaces.
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			pc.SetReadDeadline(time.Now().Add(5 * time.Second))
			n, _, src, err := pc.ReadFrom(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				break
			}
			if ua, ok := src.(*net.UDPAddr); ok {
				d.handle(buf[:n], ua.IP.String())
			}
		}
		pc.Close()
		select {
		case <-d.stop:
			return
		default:
		}
	}
}

func (d *Discovery) sleep(t time.Duration) bool {
	select {
	case <-d.stop:
		return false
	case <-time.After(t):
		return true
	}
}

func (d *Discovery) handle(b []byte, ip string) {
	var i Info
	if json.Unmarshal(b, &i) != nil || i.Fingerprint == "" || strings.EqualFold(i.Fingerprint, d.id.Fingerprint) {
		return
	}
	p := peerFrom(i, ip, "multicast", Port, true)
	d.Upsert(p)
	announce := (i.Announce != nil && *i.Announce) || (i.Announcement != nil && *i.Announcement)
	if announce && d.receiving() {
		// Answer via HTTP register (preferred by LocalSend), falling back to a multicast reply.
		go func() {
			if err := Register(context.Background(), p, d.self()); err != nil {
				no := false
				d.send(withAnnounce(d.self(), &no))
			}
		}()
	}
}

func withAnnounce(i Info, a *bool) Info {
	i.Announce, i.Announcement = a, a
	return i
}

// Announce multicasts this device so others (and LocalSend) list it and answer with their identity.
func (d *Discovery) Announce() {
	yes := true
	go d.send(withAnnounce(d.self(), &yes))
}

func (d *Discovery) send(i Info) {
	b, _ := json.Marshal(i)
	dst := &net.UDPAddr{IP: net.ParseIP(MulticastGroup), Port: Port}
	ifs := lanInterfaces()
	if len(ifs) == 0 {
		ifs = []net.Interface{{}}
	}
	for _, ifi := range ifs {
		c, err := net.ListenPacket("udp4", ":0")
		if err != nil {
			continue
		}
		pc := ipv4.NewPacketConn(c)
		pc.SetMulticastTTL(4)
		if ifi.Index != 0 {
			pc.SetMulticastInterface(&ifi)
		}
		pc.WriteTo(b, nil, dst)
		c.Close()
	}
}

// Upsert records a peer, keeping what we already knew (account link, certificate pin).
func (d *Discovery) Upsert(p Peer) {
	d.mu.Lock()
	if old, ok := d.peers[p.Key()]; ok {
		if old.Source == "server" && p.AccountDeviceID == "" {
			p.AccountDeviceID, p.Source = old.AccountDeviceID, "server"
		}
		if p.CertPin == "" {
			p.CertPin = old.CertPin
		}
		if manual(old.Source) && !manual(p.Source) {
			p.Source = old.Source
		}
	}
	p.LastSeen = time.Now().UnixMilli()
	d.peers[p.Key()] = p
	d.mu.Unlock()
	d.onChange()
}

func manual(src string) bool { return src == "manual" || src == "qr" || src == "code" }

func (d *Discovery) Remove(key string) {
	d.mu.Lock()
	delete(d.peers, key)
	d.mu.Unlock()
	d.onChange()
}

func (d *Discovery) Get(key string) (Peer, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.peers[key]
	return p, ok
}

// Peers lists known devices, most recently seen first.
func (d *Discovery) Peers() []Peer {
	d.mu.Lock()
	out := make([]Peer, 0, len(d.peers))
	for _, p := range d.peers {
		out = append(out, p)
	}
	d.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Alias) < strings.ToLower(out[j].Alias) })
	return out
}

func (d *Discovery) prune() {
	now := time.Now().UnixMilli()
	changed := false
	d.mu.Lock()
	for k, p := range d.peers {
		if !manual(p.Source) && now-p.LastSeen > 90_000 {
			delete(d.peers, k)
			changed = true
		}
	}
	d.mu.Unlock()
	if changed {
		d.onChange()
	}
}

// Connect adds a device by address (manual entry, pairing code or QR). A fingerprint from a Ferry QR
// code is the peer's certificate hash, so it is enforced as the pin.
func (d *Discovery) Connect(ctx context.Context, ip string, port int, pin, source string, https *bool) (Peer, error) {
	if port <= 0 {
		port = Port
	}
	p, err := FetchInfo(ctx, ip, port, pin, source, https, 6*time.Second)
	if err != nil {
		return Peer{}, err
	}
	if strings.EqualFold(p.Fingerprint, d.id.Fingerprint) {
		return Peer{}, errSelf
	}
	d.Upsert(p)
	return p, nil
}

var errSelf = &PeerError{Status: 400, Message: "that's this computer"}

// Scan probes every address of the local /24 networks, for networks that block multicast (like
// LocalSend's HTTP discovery). It returns when done.
func (d *Discovery) Scan(ctx context.Context) {
	if !d.scanning.CompareAndSwap(false, true) {
		return
	}
	defer d.scanning.Store(false)
	d.onChange()
	defer d.onChange()
	sem := make(chan struct{}, 64)
	var wg sync.WaitGroup
	for _, a := range LocalAddrs() {
		if a.Virtual {
			continue
		}
		ip := net.ParseIP(a.IP).To4()
		for i := 1; i < 255; i++ {
			t := net.IPv4(ip[0], ip[1], ip[2], byte(i)).String()
			if t == a.IP {
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer func() { <-sem; wg.Done() }()
				if ctx.Err() != nil {
					return
				}
				// Plain TCP first: most addresses are empty, and a TLS attempt per address is slow.
				c, err := net.DialTimeout("tcp", net.JoinHostPort(t, strconv.Itoa(Port)), 600*time.Millisecond)
				if err != nil {
					return
				}
				c.Close()
				if p, err := FetchInfo(ctx, t, Port, "", "scan", nil, 3*time.Second); err == nil && !strings.EqualFold(p.Fingerprint, d.id.Fingerprint) {
					d.Upsert(p)
				}
			}()
		}
	}
	wg.Wait()
}

// LocalAddr is a private IPv4 address of this computer.
type LocalAddr struct {
	IP      string `json:"ip"`
	Iface   string `json:"iface"`
	Virtual bool   `json:"virtual"` // Hyper-V, WSL, VirtualBox, VMware, VPN adapters
}

var virtualHints = []string{"vethernet", "virtualbox", "vmware", "wsl", "hyper-v", "docker", "loopback", "tailscale", "zerotier", "vpn", "tap-", "tun"}

func isVirtual(name string) bool {
	n := strings.ToLower(name)
	for _, h := range virtualHints {
		if strings.Contains(n, h) {
			return true
		}
	}
	return false
}

// LocalAddrs lists private IPv4 addresses, real network adapters first.
func LocalAddrs() []LocalAddr {
	var out []LocalAddr
	ifs, _ := net.Interfaces()
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				out = append(out, LocalAddr{IP: n.IP.String(), Iface: ifi.Name, Virtual: isVirtual(ifi.Name)})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Virtual != out[j].Virtual {
			return !out[i].Virtual
		}
		return rank(out[i].IP) < rank(out[j].IP)
	})
	return out
}

// rank prefers typical home/office networks (192.168.x, then 10.x, then 172.16/12).
func rank(ip string) int {
	switch {
	case strings.HasPrefix(ip, "192.168."):
		return 0
	case strings.HasPrefix(ip, "10."):
		return 1
	}
	return 2
}

func lanInterfaces() []net.Interface {
	var out []net.Interface
	ifs, _ := net.Interfaces()
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 || ifi.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && n.IP.IsPrivate() {
				out = append(out, ifi)
				break
			}
		}
	}
	return out
}
