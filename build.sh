#!/bin/sh
# Builds the release layout into dist/UrsidoRescue-<VERSION>/.
set -eu
V=$(cat VERSION)
OUT=dist/UrsidoRescue-$V
rm -rf "$OUT"; mkdir -p "$OUT"
go vet ./...
go test ./...
export CGO_ENABLED=0
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$OUT/UrsidoRescue.exe" .
GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$OUT/UrsidoRescue-linux-amd64" .
GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o "$OUT/UrsidoRescue-linux-arm64" .
cp -r payloads VERSION STATUS.md PROBE.md "$OUT/"
(cd "$OUT" && find . -type f ! -name SHA256SUMS | sort | xargs sha256sum > SHA256SUMS)
if [ "$(uname -m)" = x86_64 ]; then "$OUT/UrsidoRescue-linux-amd64" --selftest; fi
echo "built $OUT"
