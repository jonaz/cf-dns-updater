package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// version is set by the release build (-ldflags "-X main.version=...").
var version = "dev"

var (
	showVersion    = flag.Bool("version", false, "print the version and exit")
	logLevel       = flag.String("log-level", "warning", "log level (debug, info, warning, error)")
	configLocation = flag.String("config", "config.json", "location of the config file")
	once           = flag.Bool("once", false, "run one sync and exit")
)

func main() {
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(*logLevel)})))

	config, err := loadConfig(*configLocation)
	if err != nil {
		slog.Error("loading config", "error", err)
		os.Exit(1)
	}

	api, err := newAPI(config)
	if err != nil {
		slog.Error("setting up cloudflare client", "error", err)
		os.Exit(1)
	}
	lookup := newIPLookup(config.IPURLs)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if *once {
		if err := runSync(ctx, api, lookup, config.Domains); err != nil {
			os.Exit(1)
		}
		return
	}

	slog.Info("starting", "version", version, "interval", config.interval.String(), "domains", config.Domains)
	ticker := time.NewTicker(config.interval)
	defer ticker.Stop()
	runSync(ctx, api, lookup, config.Domains)
	for {
		select {
		case <-ticker.C:
			runSync(ctx, api, lookup, config.Domains)
		case <-ctx.Done():
			slog.Info("shutting down")
			return
		}
	}
}

// runSync looks up the public IP and points every domain's A record at it.
// If the lookup fails, no record is touched.
func runSync(ctx context.Context, api *api, lookup *ipLookup, domains []string) error {
	ip, err := lookup.PublicIP(ctx)
	if err != nil {
		slog.Error("looking up public ip, leaving records unchanged", "error", err)
		return err
	}
	slog.Debug("public ip", "ip", ip.String())

	var firstErr error
	for _, domain := range domains {
		if err := api.updateRecord(ctx, domain, ip); err != nil {
			slog.Error("updating record", "domain", domain, "error", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}
