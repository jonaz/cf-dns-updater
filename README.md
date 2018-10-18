# cf-dns-updater
Updates cloudflare with servers public ip

### Installation

```
go get -u github.com/jonaz/cf-dns-updater
sudo mv $GOPATH/bin/cf-dns-updater /usr/local/bin
sudo cp cf-dns-updater.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable cf-dns-updater.service
sudo systemctl start cf-dns-updater.service
```

### Flags

```
Usage of cf-dns-updater:
  -config string
    	location of the config file (default "config.json")
  -log-level string
    	log level (default "warning")
```


### Configuration
a json file that looks like this:

```
{
    "apiKey": "key",
    "apiEmail": "email",
    "interval": "30m",
    "domains": [
        "test.domain.com"
    ]
}

```
