#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
module_dir=$(dirname -- "$script_dir")
output_root="$module_dir/dist"
version="${VERSION:-0.1.0-dev}"

case "$version" in
    ""|*[!A-Za-z0-9._+-]*)
        echo "error: VERSION contains unsupported characters" >&2
        exit 1
        ;;
esac

if ! command -v go >/dev/null 2>&1; then
    echo "error: Go is required to build novascale-agent" >&2
    exit 1
fi

checksum() {
    directory=$1
    (
        cd "$directory"
        if command -v shasum >/dev/null 2>&1; then
            shasum -a 256 novascale-agent >novascale-agent.sha256
        elif command -v sha256sum >/dev/null 2>&1; then
            sha256sum novascale-agent >novascale-agent.sha256
        else
            echo "warning: no SHA-256 utility found; checksum was not written" >&2
        fi
    )
}

build_target() {
    target_os=$1
    target_arch=$2
    target_dir="$output_root/${target_os}-${target_arch}"
    mkdir -p "$target_dir"
    (
        cd "$module_dir"
        CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
            go build -buildvcs=false -trimpath \
            -ldflags="-s -w -buildid= -X main.version=${version}" \
            -o "$target_dir/novascale-agent" ./cmd/novascale-agent
    )
    checksum "$target_dir"
    echo "built $target_dir/novascale-agent"
}

build_target darwin arm64
build_target darwin amd64
build_target linux arm64
build_target linux amd64

if command -v lipo >/dev/null 2>&1; then
    universal_dir="$output_root/darwin-universal"
    mkdir -p "$universal_dir"
    lipo -create \
        "$output_root/darwin-arm64/novascale-agent" \
        "$output_root/darwin-amd64/novascale-agent" \
        -output "$universal_dir/novascale-agent"
    checksum "$universal_dir"
    echo "built $universal_dir/novascale-agent"
fi

echo "Built novascale-agent version $version."
echo "Binaries are unsigned release inputs; signing and publication are separate steps."
