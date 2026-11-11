package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const cloudflareAPI = "https://api.cloudflare.com/client/v4"

type api struct {
	baseURL string
	token   string
	key     string
	email   string
	client  *http.Client
	mu      sync.Mutex
	zoneIDs map[string]string
}

func newAPI(c *Config) (*api, error) {
	return &api{
		baseURL: cloudflareAPI,
		token:   c.APIToken,
		key:     c.APIKey,
		email:   c.APIEmail,
		client:  &http.Client{Timeout: 30 * time.Second},
		zoneIDs: map[string]string{},
	}, nil
}

type dnsRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content"`
	TTL     int    `json:"ttl,omitempty"`
	Proxied *bool  `json:"proxied,omitempty"`
}

type apiResponse struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// updateRecord points the A record(s) for domain at ip, creating one if none exists.
// Only the content is changed, so TTL and proxied settings made in the dashboard stay.
func (a *api) updateRecord(ctx context.Context, domain string, ip net.IP) error {
	zoneID, err := a.zoneID(ctx, getBaseDomain(domain))
	if err != nil {
		return err
	}

	var recs []dnsRecord
	q := url.Values{"type": {"A"}, "name": {domain}}
	if err := a.do(ctx, http.MethodGet, "/zones/"+zoneID+"/dns_records?"+q.Encode(), nil, &recs); err != nil {
		return fmt.Errorf("listing records: %w", err)
	}

	if len(recs) == 0 {
		slog.Warn("record not found, creating it", "domain", domain, "ip", ip.String())
		proxied := false
		rec := dnsRecord{Type: "A", Name: domain, Content: ip.String(), TTL: 1, Proxied: &proxied}
		return a.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", rec, nil)
	}

	for _, r := range recs {
		if r.Content == ip.String() {
			slog.Debug("record already up to date", "domain", r.Name, "ip", r.Content)
			continue
		}
		slog.Warn("updating record", "domain", r.Name, "from", r.Content, "to", ip.String())
		if err := a.do(ctx, http.MethodPatch, "/zones/"+zoneID+"/dns_records/"+r.ID, dnsRecord{Content: ip.String()}, nil); err != nil {
			return fmt.Errorf("updating %s: %w", r.Name, err)
		}
	}
	return nil
}

func (a *api) zoneID(ctx context.Context, zone string) (string, error) {
	a.mu.Lock()
	id, ok := a.zoneIDs[zone]
	a.mu.Unlock()
	if ok {
		return id, nil
	}

	var zones []struct {
		ID string `json:"id"`
	}
	if err := a.do(ctx, http.MethodGet, "/zones?"+url.Values{"name": {zone}}.Encode(), nil, &zones); err != nil {
		return "", fmt.Errorf("looking up zone %s: %w", zone, err)
	}
	if len(zones) == 0 {
		return "", fmt.Errorf("zone %s not found (does the token have access to it?)", zone)
	}

	a.mu.Lock()
	a.zoneIDs[zone] = zones[0].ID
	a.mu.Unlock()
	return zones[0].ID, nil
}

func (a *api) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	} else {
		req.Header.Set("X-Auth-Key", a.key)
		req.Header.Set("X-Auth-Email", a.email)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var r apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(&r); err != nil {
		return fmt.Errorf("%s: decoding response: %w", resp.Status, err)
	}
	if !r.Success || resp.StatusCode >= 300 {
		msgs := make([]string, 0, len(r.Errors))
		for _, e := range r.Errors {
			msgs = append(msgs, fmt.Sprintf("%d: %s", e.Code, e.Message))
		}
		return fmt.Errorf("%s: %s", resp.Status, strings.Join(msgs, "; "))
	}
	if out != nil {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func getBaseDomain(domain string) string {
	tmp := strings.Split(strings.TrimSuffix(domain, "."), ".")
	if len(tmp) < 2 {
		return domain
	}
	return tmp[len(tmp)-2] + "." + tmp[len(tmp)-1]
}
