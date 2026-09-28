#!/usr/bin/env bash
#
# Build the zju-connect native library for one target.
#
# Usage:
#   binding/build.sh [target]
#
# Targets:
#   host       (default) the current platform, as a C shared library
#   android    Android (arm64-v8a, armeabi-v7a, x86) via the NDK
#   ios        iOS as a static archive (c-shared is unsupported on ios)
#   all        android + ios + the host library
#
# Environment:
#   ANDROID_NDK_HOME  path to the Android NDK (defaults to the newest under
#                     $ANDROID_HOME/ndk)
#   OUT_DIR           output directory (defaults to ./build)
#
# The Android build applies the 16 KB page-size link flag required by
# Android 15+. Target names match the Flutter FFI plugin layout so the Dart
# side can pick the right artifact.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-${REPO_ROOT}/build}"
PKG="./binding/capi"

# Android 15 requires 16 KB aligned shared libraries.
ANDROID_LDFLAGS="-extldflags=-Wl,-z,max-page-size=16384"

log() { printf '>> %s\n' "$*"; }

build_host() {
	local os arch ext
	os="$(go env GOOS)"
	arch="$(go env GOARCH)"
	# The Dart side expects each platform's conventional file name: the loader
	# and the build hook both key on it.
	case "$os" in
		windows) name="zju_connect.dll" ;;
		darwin) name="libzju_connect.dylib" ;;
		*) name="libzju_connect.so" ;;
	esac

	log "building host library (${os}/${arch})"
	CGO_ENABLED=1 go build -buildmode=c-shared -o "${OUT_DIR}/${name}" "$PKG"
	cp "${REPO_ROOT}/binding/capi/include/zju_connect.h" "${OUT_DIR}/zju_connect.h"
	log "wrote ${OUT_DIR}/${name}"
}

android_ndk_home() {
	if [[ -n "${ANDROID_NDK_HOME:-}" ]]; then
		echo "$ANDROID_NDK_HOME"
		return
	fi
	local sdk="${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/Android/Sdk}}"
	ls -d "${sdk}"/ndk/* 2>/dev/null | sort -V | tail -1
}

build_android() {
	local ndk
	ndk="$(android_ndk_home)"
	if [[ -z "$ndk" || ! -d "$ndk" ]]; then
		echo "Android NDK not found; set ANDROID_NDK_HOME" >&2
		return 1
	fi
	local host_tag
	case "$(uname -s)" in
		Darwin) host_tag="darwin-x86_64" ;;
		*) host_tag="linux-x86_64" ;;
	esac

	local api=24
	local triple
	for triple in "arm64 arm64-v8a aarch64-linux-android" \
		"arm armeabi-v7a armv7a-linux-androideabi" \
		"386 x86 i686-linux-android"; do
		set -- $triple
		local goarch="$1" abi="$2" ctriple="$3"
		local cc="${ndk}/toolchains/llvm/prebuilt/${host_tag}/bin/${ctriple}${api}-clang"
		if [[ ! -x "$cc" ]]; then
			echo "skipping android/${goarch}: compiler $cc not found" >&2
			continue
		fi
		log "building android/${goarch} (${abi})"
		mkdir -p "${OUT_DIR}/android/${abi}"
		CGO_ENABLED=1 GOOS=android GOARCH="$goarch" CC="$cc" \
			go build -buildmode=c-shared -ldflags "$ANDROID_LDFLAGS" \
			-o "${OUT_DIR}/android/${abi}/libzju_connect.so" "$PKG"
	done
}

build_ios() {
	log "building iOS static archive"
	# iOS does not support -buildmode=c-shared; use c-archive and let the Xcode
	# target link it into an xcframework.
	for triple in "arm64 arm64-apple-ios${IOS_MIN:-13.0}" \
		"amd64 x86_64-apple-ios${IOS_MIN:-13.0}-simulator"; do
		set -- $triple
		local goarch="$1" ctriple="$2"
		local cc
		cc="$(xcrun --sdk iphoneos --find clang 2>/dev/null || true)"
		if [[ -z "$cc" ]]; then
			echo "xcrun not available; iOS build must run on macOS" >&2
			return 1
		fi
		log "building ios/${goarch}"
		mkdir -p "${OUT_DIR}/ios/${goarch}"
		CGO_ENABLED=1 GOOS=ios GOARCH="$goarch" CC="$cc" \
			go build -buildmode=c-archive \
			-o "${OUT_DIR}/ios/${goarch}/libzju_connect.a" "$PKG"
	done
}

mkdir -p "$OUT_DIR"

case "${1:-host}" in
	host) build_host ;;
	android) build_android ;;
	ios) build_ios ;;
	all)
		build_host
		build_android || true
		build_ios || true
		;;
	*)
		echo "unknown target: $1" >&2
		exit 2
		;;
esac

log "done"
