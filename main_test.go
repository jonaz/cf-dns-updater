package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestGetBaseDomain(t *testing.T) {
	for in, want := range map[string]string{
		"asdf.test.jonaz.net": "jonaz.net",
		"nergy.se":            "nergy.se",
		"t.nergy.se.":         "nergy.se",
	} {
		if got := getBaseDomain(in); got != want {
			t.Errorf("getBaseDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseIPResponse(t *testing.T) {
	trace := "fl=123\nh=1.1.1.1\nip=92.32.22.177\nts=1.2\n"
	for body, want := range map[string]string{
		trace:            "92.32.22.177",
		"92.32.22.177\n": "92.32.22.177",
	} {
		ip, err := parseIPResponse(body)
		if err != nil || ip.String() != want {
			t.Errorf("parseIPResponse(%q) = %v, %v; want %s", body, ip, err, want)
		}
	}
	if _, err := parseIPResponse("<html>oops</html>"); err == nil {
		t.Error("expected error for html body")
	}
}

func TestCheckPublicIPv4(t *testing.T) {
	ok := []string{"92.32.18.68", "8.8.8.8"}
	bad := []string{"192.168.13.3", "10.0.0.1", "172.16.5.5", "100.64.1.1", "127.0.0.1", "169.254.1.1", "0.0.0.0", "2001:db8::1"}
	for _, s := range ok {
		if err := checkPublicIPv4(net.ParseIP(s)); err != nil {
			t.Errorf("%s: unexpected error %v", s, err)
		}
	}
	for _, s := range bad {
		if err := checkPublicIPv4(net.ParseIP(s)); err == nil {
			t.Errorf("%s: expected it to be refused", s)
		}
	}
}

func TestPublicIPFallsBack(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer broken.Close()
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ip=192.168.13.3\n")
	}))
	defer private.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "92.32.22.177\n")
	}))
	defer good.Close()

	ip, err := newIPLookup([]string{broken.URL, private.URL, good.URL}).PublicIP(context.Background())
	if err != nil || ip.String() != "92.32.22.177" {
		t.Fatalf("got %v, %v", ip, err)
	}
	if _, err := newIPLookup([]string{broken.URL, private.URL}).PublicIP(context.Background()); err == nil {
		t.Fatal("expected error when no source gives a public ip")
	}
}

// fakeCloudflare serves just enough of the v4 API for updateRecord.
type fakeCloudflare struct {
	mu      sync.Mutex
	records map[string]*dnsRecord // by id
	calls   []string
	auth    []string
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.auth = append(f.auth, r.Header.Get("Authorization")+"|"+r.Header.Get("X-Auth-Key"))
	reply := func(v any) {
		b, _ := json.Marshal(v)
		json.NewEncoder(w).Encode(apiResponse{Success: true, Result: b})
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/zones":
		if r.URL.Query().Get("name") != "nergy.se" {
			reply([]any{})
			return
		}
		reply([]map[string]string{{"id": "zone1"}})
	case r.Method == http.MethodGet && r.URL.Path == "/zones/zone1/dns_records":
		var out []dnsRecord
		for _, rec := range f.records {
			if rec.Name == r.URL.Query().Get("name") {
				out = append(out, *rec)
			}
		}
		reply(out)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/zones/zone1/dns_records/"):
		var in dnsRecord
		json.NewDecoder(r.Body).Decode(&in)
		rec := f.records[strings.TrimPrefix(r.URL.Path, "/zones/zone1/dns_records/")]
		if rec == nil {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(apiResponse{Errors: []apiError{{Code: 81044, Message: "not found"}}})
			return
		}
		rec.Content = in.Content
		reply(rec)
	case r.Method == http.MethodPost && r.URL.Path == "/zones/zone1/dns_records":
		var in dnsRecord
		json.NewDecoder(r.Body).Decode(&in)
		in.ID = "new"
		f.records[in.ID] = &in
		reply(in)
	default:
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(apiResponse{Errors: []apiError{{Code: 10000, Message: "Authentication error"}}})
	}
}

func newTestAPI(t *testing.T, f *fakeCloudflare, c *Config) *api {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	a, _ := newAPI(c)
	a.baseURL = srv.URL
	return a
}

func TestUpdateRecord(t *testing.T) {
	f := &fakeCloudflare{records: map[string]*dnsRecord{
		"r1": {ID: "r1", Type: "A", Name: "nergy.se", Content: "92.32.18.68"},
		"r2": {ID: "r2", Type: "A", Name: "t.nergy.se", Content: "92.32.22.177"},
	}}
	a := newTestAPI(t, f, &Config{APIToken: "tok"})
	ip := net.ParseIP("92.32.22.177")
	ctx := context.Background()

	for _, d := range []string{"nergy.se", "t.nergy.se", "new.nergy.se"} {
		if err := a.updateRecord(ctx, d, ip); err != nil {
			t.Fatalf("%s: %v", d, err)
		}
	}
	if got := f.records["r1"].Content; got != "92.32.22.177" {
		t.Errorf("r1 not updated: %s", got)
	}
	if rec := f.records["new"]; rec == nil || rec.Content != "92.32.22.177" || rec.Name != "new.nergy.se" {
		t.Errorf("record not created: %+v", rec)
	}
	patches, zoneLookups := 0, 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "PATCH") {
			patches++
		}
		if c == "GET /zones" {
			zoneLookups++
		}
	}
	if patches != 1 {
		t.Errorf("want 1 PATCH (t.nergy.se was already right), got %d: %v", patches, f.calls)
	}
	if zoneLookups != 1 {
		t.Errorf("zone id should be cached, got %d lookups", zoneLookups)
	}
	if f.auth[0] != "Bearer tok|" {
		t.Errorf("token auth header: %q", f.auth[0])
	}
}

func TestUpdateRecordErrors(t *testing.T) {
	f := &fakeCloudflare{records: map[string]*dnsRecord{}}
	a := newTestAPI(t, f, &Config{APIKey: "k", APIEmail: "e"})
	err := a.updateRecord(context.Background(), "jonaz.net", net.ParseIP("92.32.22.177"))
	if err == nil || !strings.Contains(err.Error(), "zone jonaz.net not found") {
		t.Fatalf("got %v", err)
	}
	if f.auth[0] != "|k" {
		t.Errorf("legacy auth header: %q", f.auth[0])
	}
}

func TestRunSyncSkipsOnLookupFailure(t *testing.T) {
	f := &fakeCloudflare{records: map[string]*dnsRecord{}}
	a := newTestAPI(t, f, &Config{APIToken: "tok"})
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "192.168.13.3")
	}))
	defer private.Close()

	if err := runSync(context.Background(), a, newIPLookup([]string{private.URL}), []string{"nergy.se"}); err == nil {
		t.Fatal("expected error")
	}
	if len(f.calls) != 0 {
		t.Fatalf("cloudflare must not be called, got %v", f.calls)
	}
}

func TestConfigValidate(t *testing.T) {
	c := &Config{APIToken: "t", Domains: []string{"nergy.se"}}
	if err := c.validate(); err != nil || c.interval.String() != "10m0s" || len(c.IPURLs) != 2 {
		t.Fatalf("defaults: %v %+v", err, c)
	}
	for name, c := range map[string]*Config{
		"no auth":    {Domains: []string{"nergy.se"}},
		"no domains": {APIToken: "t"},
		"short":      {APIToken: "t", Domains: []string{"x.se"}, Interval: "1s"},
		"both":       {APIToken: "t", APITokenFile: "/x", Domains: []string{"x.se"}},
	} {
		if err := c.validate(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
