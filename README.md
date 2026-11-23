# fiche-go
Command line pastebin for sharing terminal output and files (text or binary). Go version!

Based on [fiche](https://github.com/solusipse/fiche), originally written in C, and compatible
with its client usage and flags (`-d -o -p -L -s -S -B -l`).

## Features
- Text **and binary** pastes: UTF-8 text is saved as `index.txt`, anything else as `index.bin`
- Data is streamed straight to disk (constant memory per connection, even for large pastes)
- Size limit (`-B`, default 50 MiB): bigger pastes are rejected with a message, never truncated
- Concurrent connections with global and per-IP limits
- Idle timeout (works with clients that never half-close, like traditional `nc`) and total timeout
- Refuses pastes when disk free space is low
- Random slugs from `crypto/rand`, created atomically (collision-safe)
- Graceful shutdown on SIGTERM

## What's new
Compared with the first version of fiche-go:

| | v1 | now |
|---|---|---|
| Connections | one at a time (port closed between them) | concurrent, with global and per-IP limits |
| Paste size | unlimited, read fully into memory | limited (`-B`), streamed to disk |
| Binary files | saved, but always as `index.txt` | detected and saved as `index.bin` |
| Errors (e.g. slug collision) | stopped the server | handled, server keeps running |
| Reply to client | `Diretório criado: xxxx` | paste URL, like fiche (`https://domain/xxxx/`) |
| Configuration | hardcoded | fiche-compatible flags |
| Slugs | `math/rand` (predictable) | `crypto/rand` |

Compared with fiche in C: binaries are no longer cut at the first NUL byte, large `-B` values no
longer overflow the thread stack, and pastes over the limit are rejected instead of silently truncated.

## Build
```
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o fiche-go .
```

## Run
```
./fiche-go -d bin.example.com -S -o /srv/bin -B 52428800
```

| Flag | Default | Description |
|---|---|---|
| `-d` | `localhost` | domain used in the returned URL |
| `-S` | off | return `https://` URLs |
| `-o` | `./code` | output directory (must exist) |
| `-L` / `-p` | `0.0.0.0` / `9999` | listen address / port |
| `-s` | `4` | slug length |
| `-B` | `52428800` | max paste size in bytes |
| `-l` | | log file (besides stdout) |
| `-max-conns` | `64` | simultaneous connections |
| `-max-per-ip` | `4` | simultaneous connections per IP |
| `-idle` | `5s` | end of paste after this long without data |
| `-timeout` | `3m` | max duration of a connection |
| `-min-free` | `2048` | refuse pastes below this many MB free on disk |

Serve the output directory with any web server. Example for nginx (binaries always downloaded):
```nginx
root /srv/bin;
index index.txt index.bin;
add_header X-Content-Type-Options nosniff always;
location ~ ^/(?<slug>[a-z0-9]+)/index\.bin$ {
    default_type application/octet-stream;
    types { }
    add_header Content-Disposition 'attachment; filename="$slug.bin"' always;
    add_header X-Content-Type-Options nosniff always;
}
location ~ /\. { deny all; }
```

### Text or binary
The first 8 KiB of each paste are inspected: valid UTF-8 without NUL bytes is text (`index.txt`,
shown in the browser); anything else is binary (`index.bin`, served as a download). While a paste
is being received it is written to `.upload` inside the slug directory and renamed when complete.

### Replies and errors
| Situation | Reply to the client |
|---|---|
| success | `https://domain/xxxx/` |
| paste bigger than `-B` | `ERROR: paste too large (max N bytes)` |
| nothing sent | `ERROR: empty paste` |
| per-IP limit reached | `ERROR: too many simultaneous connections from your IP` |
| global limit reached | `ERROR: server busy, try again later` |
| low disk space | `ERROR: server out of space, try again later` |

Log line per paste (stdout and `-l` file): `2026/11/22 01:06:31 o2tr -- 203.0.113.7 -- 8000000 bytes (binario)`

## Running as a systemd service
Dedicated user, output directory and hardened unit (`/etc/systemd/system/fiche-go.service`):
```
useradd -r -s /sbin/nologin -d /srv/bin fiche && mkdir -p /srv/bin && chown fiche: /srv/bin
install -m755 fiche-go /usr/local/bin/
```
```ini
[Unit]
Description=fiche-go pastebin
After=network.target

[Service]
User=fiche
Group=fiche
UMask=0022
LogsDirectory=fiche-go
ExecStart=/usr/local/bin/fiche-go -d bin.example.com -S -o /srv/bin -l /var/log/fiche-go/fiche-go.log -B 52428800
Restart=always
RestartSec=2
MemoryMax=256M
TasksMax=512
LimitNOFILE=4096
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/srv/bin
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

### Expiring old pastes
fiche-go does not delete pastes. A systemd timer removing pastes older than 12 hours every 15 minutes:

`/etc/systemd/system/fiche-go-cleanup.service`
```ini
[Unit]
Description=Remove fiche-go pastes older than 12 hours

[Service]
Type=oneshot
User=fiche
Group=fiche
ExecStart=/usr/bin/find /srv/bin -mindepth 1 -maxdepth 1 -type d -regextype posix-extended -regex '.*/[a-z0-9]+' -mmin +720 -print -exec rm -rf {} +
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=/srv/bin
```
`/etc/systemd/system/fiche-go-cleanup.timer`
```ini
[Unit]
Description=Periodic cleanup of fiche-go pastes

[Timer]
OnBootSec=5min
OnUnitActiveSec=15min
Persistent=true

[Install]
WantedBy=timers.target
```
```
systemctl daemon-reload && systemctl enable --now fiche-go fiche-go-cleanup.timer
```
Change `-mmin +720` (minutes) to keep pastes for longer or shorter.

## Client-side usage
```
echo just testing! | nc bin.example.com 9999
cat file.txt | nc bin.example.com 9999
nc bin.example.com 9999 < backup.tar.gz
curl -o backup.tar.gz https://bin.example.com/abcd/
```
With traditional `nc` (no half-close) the URL arrives after the idle timeout; use `nc -N` or `ncat` to get it immediately.
