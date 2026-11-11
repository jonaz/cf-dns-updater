package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
	return &ipLookup{urls: urls, client: &http.Client{Timeout: 10 * time.Second}}
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

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// checkPublicIPv4 refuses anything that must never end up in a public A record.
func checkPublicIPv4(ip net.IP) error {
	v4 := ip.To4()
	switch {
	case v4 == nil:
		return fmt.Errorf("%s is not IPv4", ip)
	case !v4.IsGlobalUnicast(), v4.IsPrivate(), cgnat.Contains(v4):
		return fmt.Errorf("%s is not a public address", ip)
	}
	return nil
}
