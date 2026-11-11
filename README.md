# cf-dns-updater
Keeps Cloudflare A records pointed at the public IP of the network it runs in.

The public IP is asked from outside (Cloudflare's `/cdn-cgi/trace`, then icanhazip), so it works
on a host behind NAT or a port-forward, not only on a host with the public IP on an interface.
An address that is private, CGNAT, loopback, link-local or another special-use range (documentation,
benchmarking, multicast, reserved) is refused, and no record is touched when no lookup succeeds.
The lookups ignore `HTTPS_PROXY`, since a proxy would report its own address. Running it on
several hosts behind the same router is safe: they all write the same value.

It only changes a record's content, so TTL and proxied settings made in the dashboard are kept.
A missing record is created (unproxied, auto TTL).

### Installation

Download `cf-dns-updater-linux-amd64` (or `-arm64`) and `SHA256SUMS` from the
[latest release](https://github.com/jonaz/cf-dns-updater/releases/latest), check it with
`sha256sum -c --ignore-missing SHA256SUMS`, and install it as `/usr/local/bin/cf-dns-updater`.
Or build it yourself:

```
go install github.com/jonaz/cf-dns-updater@latest
sudo mv $(go env GOPATH)/bin/cf-dns-updater /usr/local/bin

sudo curl -o /etc/systemd/system/cf-dns-updater.service https://raw.githubusercontent.com/jonaz/cf-dns-updater/master/cf-dns-updater.service

sudo groupadd cloudflare
sudo useradd -r -g cloudflare cloudflare

sudo systemctl daemon-reload
sudo systemctl enable --now cf-dns-updater.service
```

### Flags

```
Usage of cf-dns-updater:
  -config string
    	location of the config file (default "config.json")
  -log-level string
    	log level (debug, info, warning, error) (default "warning")
  -once
    	run one sync and exit
  -version
    	print the version and exit
```

Updates are logged at warning, so the default level shows every change.

### Configuration

Use an API token scoped to **Zone → DNS → Edit** for the zones you list
(plus **Zone → Zone → Read**, so the zone ID can be looked up):

```json
{
    "apiTokenFile": "/etc/cf-dns-updater.token",
    "interval": "5m",
    "domains": [
        "nergy.se",
        "t.nergy.se"
    ]
}
```

| Field | |
|---|---|
| `apiToken` / `apiTokenFile` | the token, inline or in a file (not both) |
| `apiKey` + `apiEmail` | legacy global API key, still supported |
| `interval` | how often to sync, at least 30s (default `10m`) |
| `domains` | A records to keep updated; the zone is the last two labels |
| `ipUrls` | optional, overrides the IP lookup URLs; each returns a bare IP or an `ip=` line |

Test it with `cf-dns-updater -config /etc/cf-dns-updater.json -once -log-level debug`.
