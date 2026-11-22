# fiche-go
Command line pastebin for sharing terminal output and files (text or binary). Go version!

Based on [fiche](https://github.com/solusipse/fiche), originally written in C, and compatible
with its client usage and flags (`-d -o -p -L -s -S -B -l`).

## Features
- Text **and binary** pastes: UTF-8 text is saved as `index.txt`, anything else as `index.bin`
- Data is streamed straight to disk (constant memory per connection, even for large pastes)
- Size limit (`-B`, default 10 MiB): bigger pastes are rejected with a message, never truncated
- Concurrent connections with global and per-IP limits
- Idle timeout (works with clients that never half-close, like traditional `nc`) and total timeout
- Refuses pastes when disk free space is low
- Random slugs from `crypto/rand`, created atomically (collision-safe)
- Graceful shutdown on SIGTERM

## Build
```
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o fiche-go .
```

## Run
```
./fiche-go -d bin.example.com -S -o /srv/bin -B 10485760
```

| Flag | Default | Description |
|---|---|---|
| `-d` | `localhost` | domain used in the returned URL |
| `-S` | off | return `https://` URLs |
| `-o` | `./code` | output directory (must exist) |
| `-L` / `-p` | `0.0.0.0` / `9999` | listen address / port |
| `-s` | `4` | slug length |
| `-B` | `10485760` | max paste size in bytes |
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

Old pastes are not removed by fiche-go; use a cron job or systemd timer, e.g. delete after 12 hours:
```
find /srv/bin -mindepth 1 -maxdepth 1 -type d -mmin +720 -exec rm -rf {} +
```

## Client-side usage
```
echo just testing! | nc bin.example.com 9999
cat file.txt | nc bin.example.com 9999
nc bin.example.com 9999 < backup.tar.gz
curl -o backup.tar.gz https://bin.example.com/abcd/
```
With traditional `nc` (no half-close) the URL arrives after the idle timeout; use `nc -N` or `ncat` to get it immediately.
