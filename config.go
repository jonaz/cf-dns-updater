package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	// APIToken is a scoped Cloudflare API token (Zone > DNS > Edit). Preferred.
	APIToken string `json:"apiToken"`
	// APITokenFile reads the token from a file instead, so the config itself can be public.
	APITokenFile string `json:"apiTokenFile"`
	// APIKey and APIEmail are the legacy global API key login.
	APIKey   string `json:"apiKey"`
	APIEmail string `json:"apiEmail"`

	Interval string   `json:"interval"`
	Domains  []string `json:"domains"`
	// IPURLs are asked in order for the public IP. Defaults to Cloudflare's trace and icanhazip.
	IPURLs []string `json:"ipUrls"`

	interval time.Duration
}

func loadConfig(fn string) (*Config, error) {
	b, err := os.ReadFile(fn)
	if err != nil {
		return nil, err
	}
	config := &Config{}
	if err := json.Unmarshal(b, config); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", fn, err)
	}
	return config, config.validate()
}

func (c *Config) validate() error {
	if c.Interval == "" {
		c.Interval = "10m"
	}
	d, err := time.ParseDuration(c.Interval)
	if err != nil {
		return fmt.Errorf("interval: %w", err)
	}
	if d < 30*time.Second {
		return fmt.Errorf("interval %s is shorter than 30s", d)
	}
	c.interval = d

	if c.APITokenFile != "" {
		if c.APIToken != "" {
			return errors.New("set apiToken or apiTokenFile, not both")
		}
		b, err := os.ReadFile(c.APITokenFile)
		if err != nil {
			return fmt.Errorf("apiTokenFile: %w", err)
		}
		c.APIToken = strings.TrimSpace(string(b))
	}
	if c.APIToken == "" && (c.APIKey == "" || c.APIEmail == "") {
		return errors.New("set apiToken, apiTokenFile, or both apiKey and apiEmail")
	}
	if len(c.Domains) == 0 {
		return errors.New("no domains configured")
	}
	if len(c.IPURLs) == 0 {
		c.IPURLs = defaultIPURLs
	}
	return nil
}
