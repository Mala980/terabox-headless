# terabox-headless

Headless browser CLI untuk Terabox — lightweight, static binary untuk Linux arm64.

## Build

```
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -trimpath -o terabox-headless-arm64 .
```

## Usage

```
terabox login              Login via QR code
terabox logout             Clear session
terabox status             Check login status + quota
terabox ls [/dir]          List files
terabox upload <file> [dir]  Upload file
terabox dl <remote> [local]  Download file
```

Session disimpan di `~/.config/terabox/session.json`.

## License

MIT