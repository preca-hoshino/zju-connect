#!/usr/bin/env bash
#
# Build the zju-connect native library.
#
# Usage:
#   binding/build.sh [target...]
#
# Targets:
#   host       the current platform, as a C shared library       (default)
#   android    Android ABIs (arm64-v8a, armeabi-v7a, x86) via the NDK
#   ios        iOS static archives; must run on macOS
#   all        host + android + ios
#
# Every artifact is written to the layout that flutter/sangfor_vpn_client's
# build hook looks for (`native/<os>-<arch>/<file>`) plus, for the host build,
# a copy at the top level for the C smoke test:
#
#   <out>/host/<file>              convenience copy of the host build
#   <out>/windows-x64/<file>
#   <out>/linux-x64/<file>
#   <out>/linux-arm64/<file>
#   <out>/macos-arm64/<file>
#   <out>/ios-arm64/<file>         static archive (c-archive)
#   <out>/ios-x64/<file>           simulator, static archive
#   <out>/android-arm64/<file>
#   <out>/android-arm/<file>
#   <out>/android-ia32/<file>
#
# Environment:
#   OUT_DIR            output root (default: <repo>/build/native)
#   ANDROID_NDK_HOME   Android NDK path (default: newest under $ANDROID_HOME/ndk)
#   IOS_MIN            minimum iOS version for the archive (default: 13.0)
#
# The Android build applies the 16 KB page-size link flag required by
# Android 15+.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-${REPO_ROOT}/build/native}"
PKG="./binding/capi"
HEADER="${REPO_ROOT}/binding/capi/include/zju_connect.h"

# Android 15 requires 16 KB aligned shared libraries.
ANDROID_LDFLAGS="-extldflags=-Wl,-z,max-page-size=16384"
ANDROID_API=24

log() { printf '>> %s\n' "$*"; }

# Maps a GOOS/GOARCH pair to the <os>-<arch> key used by the Dart side.
target_key() {
	local os="$1" arch="$2"
	local dart_os dart_arch
	case "$os" in
		windows) dart_os=windows ;;
		darwin) dart_os=macos ;;
		linux) dart_os=linux ;;
		android) dart_os=android ;;
		ios) dart_os=ios ;;
		*) dart_os="$os" ;;
	esac
	case "$arch" in
		amd64) dart_arch=x64 ;;
		arm64) dart_arch=arm64 ;;
		arm) dart_arch=arm ;;
		386) dart_arch=ia32 ;;
		*) dart_arch="$arch" ;;
	esac
	echo "${dart_os}-${dart_arch}"
}

# The file name each platform's dynamic loader expects.
library_file_name() {
	case "$1" in
		windows) echo "zju_connect.dll" ;;
		darwin) echo "libzju_connect.dylib" ;;
		*) echo "libzju_connect.so" ;;
	esac
}

build_host() {
	local os arch key name
	os="$(go env GOOS)"
	arch="$(go env GOARCH)"
	key="$(target_key "$os" "$arch")"
	name="$(library_file_name "$os")"

	log "building host library (${os}/${arch} -> ${key})"
	mkdir -p "${OUT_DIR}/${key}" "${OUT_DIR}/host"
	CGO_ENABLED=1 go build -buildmode=c-shared \
		-o "${OUT_DIR}/${key}/${name}" "$PKG"
	cp "$HEADER" "${OUT_DIR}/${key}/zju_connect.h"
	# A stable top-level path for the C smoke test and for
	# `ZJU_CONNECT_LIBRARY_DIR`.
	cp "${OUT_DIR}/${key}/${name}" "${OUT_DIR}/host/${name}"
	cp "$HEADER" "${OUT_DIR}/host/zju_connect.h"
	log "wrote ${OUT_DIR}/${key}/${name}"
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

	local triple
	for triple in "arm64 aarch64-linux-android" \
		"arm armv7a-linux-androideabi" \
		"386 i686-linux-android"; do
		set -- $triple
		local goarch="$1" ctriple="$2"
		local key cc
		key="$(target_key android "$goarch")"
		cc="${ndk}/toolchains/llvm/prebuilt/${host_tag}/bin/${ctriple}${ANDROID_API}-clang"
		if [[ ! -x "$cc" ]]; then
			echo "skipping ${key}: compiler $cc not found" >&2
			continue
		fi
		log "building android/${goarch} (${key})"
		mkdir -p "${OUT_DIR}/${key}"
		CGO_ENABLED=1 GOOS=android GOARCH="$goarch" CC="$cc" \
			go build -buildmode=c-shared -ldflags "$ANDROID_LDFLAGS" \
			-o "${OUT_DIR}/${key}/libzju_connect.so" "$PKG"
		cp "$HEADER" "${OUT_DIR}/${key}/zju_connect.h"
	done
}

build_ios() {
	if ! command -v xcrun >/dev/null 2>&1; then
		echo "xcrun not available; the iOS build must run on macOS" >&2
		return 1
	fi

	local min="${IOS_MIN:-13.0}" triple
	for triple in "arm64 arm64-apple-ios${min}" \
		"amd64 x86_64-apple-ios${min}-simulator"; do
		set -- $triple
		local goarch="$1" ctriple="$2"
		local key cc sdk sdk_path
		key="$(target_key ios "$goarch")"
		if [[ "$goarch" == "amd64" ]]; then
			sdk=iphonesimulator
		else
			sdk=iphoneos
		fi
		cc="$(xcrun --sdk "$sdk" --find clang)"
		# The Go toolchain does not configure the iOS sysroot itself (that is
		# gomobile's job), so pass it explicitly or the C compile step fails to
		# find the iOS headers.
		sdk_path="$(xcrun --sdk "$sdk" --show-sdk-path)"
		log "building ios/${goarch} (${key}, ${sdk})"
		mkdir -p "${OUT_DIR}/${key}"
		# iOS has no -buildmode=c-shared; a c-archive is linked into the app.
		CGO_ENABLED=1 GOOS=ios GOARCH="$goarch" CC="$cc" \
			CGO_CFLAGS="-isysroot ${sdk_path}" \
			CGO_LDFLAGS="-isysroot ${sdk_path}" \
			go build -buildmode=c-archive \
			-o "${OUT_DIR}/${key}/libzju_connect.a" "$PKG"
		cp "$HEADER" "${OUT_DIR}/${key}/zju_connect.h"
	done
}

mkdir -p "$OUT_DIR"

if [[ $# -eq 0 ]]; then
	set -- host
fi

for target in "$@"; do
	case "$target" in
		host) build_host ;;
		android) build_android ;;
		ios) build_ios ;;
		all)
			build_host
			build_android || true
			build_ios || true
			;;
		*)
			echo "unknown target: $target" >&2
			exit 2
			;;
	esac
done

log "done -> ${OUT_DIR}"
