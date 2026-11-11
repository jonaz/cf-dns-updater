package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

var defaultIPURLs = []string{
	"https://1.1.1.1/cdn-cgi/trace",
	"https://ipv4.icanhazip.com",
}

// ipLookup asks outside services which address our traffic comes from, so it
// works behind NAT. The old version used the local socket address, which is
// only the public IP when the host itself has it on an interface.
type ipLookup struct {
	urls   []string
	client *http.Client
}

func newIPLookup(urls []string) *ipLookup {
	// Never use HTTP(S)_PROXY here: the services would report the proxy's
	// address, which would then be published in every A record.
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &ipLookup{urls: urls, client: &http.Client{Timeout: 10 * time.Second, Transport: t}}
}

// PublicIP returns the first valid public IPv4 address any URL reports.
func (l *ipLookup) PublicIP(ctx context.Context) (net.IP, error) {
	var errs []error
	for _, u := range l.urls {
		ip, err := l.fetch(ctx, u)
		if err == nil {
			return ip, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", u, err))
	}
	return nil, errors.Join(errs...)
}

func (l *ipLookup) fetch(ctx context.Context, url string) (net.IP, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, err
	}
	ip, err := parseIPResponse(string(body))
	if err != nil {
		return nil, err
	}
	return ip, checkPublicIPv4(ip)
}

// parseIPResponse accepts Cloudflare's trace format (an "ip=" line) or a bare address.
func parseIPResponse(body string) (net.IP, error) {
	s := bufio.NewScanner(strings.NewReader(body))
	for s.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(s.Text()), "ip="); ok {
			return parseIP(v)
		}
	}
	return parseIP(strings.TrimSpace(body))
}

func parseIP(s string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return nil, fmt.Errorf("not an ip address: %.40q", s)
	}
	return ip, nil
}

// nonPublicV4 lists the IANA special-purpose IPv4 ranges that are not
// globally reachable, plus multicast and reserved space.
var nonPublicV4 = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),       // "this network"
	netip.MustParsePrefix("10.0.0.0/8"),      // private
	netip.MustParsePrefix("100.64.0.0/10"),   // CGNAT
	netip.MustParsePrefix("127.0.0.0/8"),     // loopback
	netip.MustParsePrefix("169.254.0.0/16"),  // link local
	netip.MustParsePrefix("172.16.0.0/12"),   // private
	netip.MustParsePrefix("192.0.0.0/24"),    // IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("192.88.99.0/24"),  // deprecated 6to4 relay anycast
	netip.MustParsePrefix("192.168.0.0/16"),  // private
	netip.MustParsePrefix("198.18.0.0/15"),   // benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("224.0.0.0/4"),     // multicast
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved, includes broadcast
}

// checkPublicIPv4 refuses anything that must never end up in a public A record.
func checkPublicIPv4(ip net.IP) error {
	v4 := ip.To4()
	if v4 == nil {
		return fmt.Errorf("%s is not IPv4", ip)
	}
	addr := netip.AddrFrom4([4]byte(v4))
	for _, p := range nonPublicV4 {
		if p.Contains(addr) {
			return fmt.Errorf("%s is not a public address (%s)", ip, p)
		}
	}
	return nil
}
