#!/usr/bin/env bash
# Builds the standalone algokit binaries for every supported target. Go can
# cross-compile all of them from a single runner, and with CGO disabled they
# are static.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

outdir="${OUTDIR:-dist}"
version="${VERSION:?VERSION must be set}"

rm -rf "$outdir"
mkdir -p "$outdir"

ldflags="-s -w -X github.com/joe-p/algokit-cli-go/internal/commands.Version=${version}"

# goos/goarch/asset-arch triples. Asset names use x64 rather than amd64.
for spec in linux/amd64/x64 linux/arm64/arm64 darwin/amd64/x64 darwin/arm64/arm64 windows/amd64/x64 windows/arm64/arm64; do
  IFS=/ read -r goos goarch assetarch <<<"$spec"
  ext=""
  [ "$goos" = "windows" ] && ext=".exe"
  name="algokit-${goos}-${assetarch}${ext}"
  echo "Building ${goos}/${goarch} -> ${outdir}/${name}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -ldflags "$ldflags" -o "${outdir}/${name}" ./cmd/algokit
done

echo "Built standalone binaries for v${version} in ${outdir}"
