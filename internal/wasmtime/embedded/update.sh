#!/usr/bin/env bash
# Downloads the Wasmtime C API release artifacts and refreshes the embedded,
# zstd-compressed shared libraries. Bump `Version` in embedded.go to match.
#
# Usage: ./update.sh v50.0.0
set -euo pipefail

version="${1:?usage: $0 <version, e.g. v50.0.0>}"
here="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fetch() { # <release-target> <goos_goarch> <lib-name>
	local target="$1" plat="$2" name="$3" ext="tar.xz"
	[[ "$target" == *windows* ]] && ext="zip"
	local file="wasmtime-${version}-${target}-c-api"
	curl -sSLf -o "$work/$file.$ext" \
		"https://github.com/bytecodealliance/wasmtime/releases/download/${version}/${file}.${ext}"
	if [[ "$ext" == "zip" ]]; then
		unzip -q -o "$work/$file.$ext" -d "$work"
	else
		tar -C "$work" -xf "$work/$file.$ext"
	fi
	mkdir -p "$here/$plat"
	zstd -19 --long=27 -q -f "$work/$file/lib/$name" -o "$here/$plat/$name.zst"

	local digest
	digest="$(shasum -a 256 "$here/$plat/$name.zst" | cut -c1-16)"
	cat >"$here/lib_${plat}.go" <<GO
//go:build ${plat%_*} && ${plat#*_} && !wasmtime_noembed

package embedded

import _ "embed"

//go:embed ${plat}/${name}.zst
var compressed []byte

// digest identifies the embedded library; see Digest.
const digest = "${digest}"

// Name is the platform specific file name of the Wasmtime shared library.
const Name = "${name}"
GO
	echo "updated $plat/$name.zst"
}

fetch aarch64-macos darwin_arm64 libwasmtime.dylib
fetch x86_64-macos darwin_amd64 libwasmtime.dylib
fetch aarch64-linux linux_arm64 libwasmtime.so
fetch x86_64-linux linux_amd64 libwasmtime.so
fetch x86_64-windows windows_amd64 wasmtime.dll
fetch aarch64-windows windows_arm64 wasmtime.dll
