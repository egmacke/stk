#!/bin/sh
#
# Installs a prebuilt stk binary for linux or darwin.
#
#   curl -fsSL https://raw.githubusercontent.com/egmacke/stk/main/install.sh | sh
#
# Environment:
#   STK_VERSION       tag to install, e.g. v0.1.0 (default: the latest release)
#   STK_INSTALL_DIR   where to put the binary (default: ~/.local/bin)
#
# Every archive is checked against the release's own checksums.txt before it is
# unpacked. The script installs nothing it could not verify, and never asks for
# root: it writes to a directory you already own.

set -eu

REPO="egmacke/stk"
BINARY="stk"
INSTALL_DIR="${STK_INSTALL_DIR:-$HOME/.local/bin}"

log()  { printf '%s\n' "$*" >&2; }
die()  { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------- downloading

if command -v curl >/dev/null 2>&1; then
	fetch_file() { curl -fsSL "$1" -o "$2"; }
	# Resolve the latest tag from the redirect on /releases/latest rather than
	# the API, which rate-limits unauthenticated callers to 60 requests an hour
	# and would fail on a shared network.
	resolve_latest() { curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest"; }
elif command -v wget >/dev/null 2>&1; then
	fetch_file() { wget -qO "$2" "$1"; }
	resolve_latest() {
		wget -q -S -O /dev/null "https://github.com/$REPO/releases/latest" 2>&1 \
			| awk '/^[ \t]*Location:/ { print $2 }' | tail -n 1
	}
else
	die "neither curl nor wget is installed"
fi

# ------------------------------------------------------------------ platform

detect_platform() {
	os=$(uname -s | tr '[:upper:]' '[:lower:]')
	arch=$(uname -m)

	case "$os" in
		linux)  ;;
		darwin) ;;
		*) die "unsupported operating system: $os (stk ships linux and darwin binaries; build from source with 'make install')" ;;
	esac

	case "$arch" in
		x86_64|amd64)  arch=amd64 ;;
		aarch64|arm64) arch=arm64 ;;
		*) die "unsupported architecture: $arch (stk ships amd64 and arm64 binaries; build from source with 'make install')" ;;
	esac

	printf '%s_%s' "$os" "$arch"
}

# ------------------------------------------------------------------ checksums

# sha256 prints the digest of a file, using whichever tool the system has.
sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | awk '{ print $NF }'
	else
		die "no sha256 tool found (install coreutils or openssl); refusing to install an unverified binary"
	fi
}

verify() {
	archive=$1 sums=$2 name=$3

	expected=$(awk -v want="$name" '$2 == want || $2 == "*" want { print $1 }' "$sums" | head -n 1)
	[ -n "$expected" ] || die "$name is not listed in checksums.txt; refusing to install"

	actual=$(sha256 "$archive")
	[ "$expected" = "$actual" ] || die "checksum mismatch for $name
  expected $expected
  actual   $actual
The download is corrupt or has been tampered with. Nothing was installed."
}

# ----------------------------------------------------------------------- main

platform=$(detect_platform)

version="${STK_VERSION:-}"
if [ -z "$version" ]; then
	log "Resolving the latest release..."
	url=$(resolve_latest) || die "could not reach github.com"
	version=${url##*/}
	case "$version" in
		v*) ;;
		*) die "could not determine the latest version (got '$version')" ;;
	esac
fi

archive="${BINARY}_${version}_${platform}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t stk)
trap 'rm -rf "$tmp"' EXIT INT TERM

log "Downloading $BINARY $version ($platform)..."
fetch_file "$base/$archive" "$tmp/$archive" \
	|| die "could not download $base/$archive
Check that $version exists and ships a $platform build:
  https://github.com/$REPO/releases"
fetch_file "$base/checksums.txt" "$tmp/checksums.txt" \
	|| die "could not download the checksums for $version; refusing to install an unverified binary"

log "Verifying checksum..."
verify "$tmp/$archive" "$tmp/checksums.txt" "$archive"

# Unpacked from inside the temporary directory rather than with tar -C, whose
# handling of an archive path given alongside a directory change differs
# between GNU tar and the bsdtar on macOS.
(cd "$tmp" && tar -xzf "$archive") || die "could not unpack $archive"
[ -f "$tmp/$BINARY" ] || die "$archive did not contain a $BINARY binary"

mkdir -p "$INSTALL_DIR" || die "could not create $INSTALL_DIR"
[ -w "$INSTALL_DIR" ] || die "$INSTALL_DIR is not writable
Choose somewhere you own:
  STK_INSTALL_DIR=\$HOME/bin sh install.sh"

# Install through a temporary name in the destination directory so an
# interrupted copy can never leave a half-written binary on PATH, and so
# replacing a running stk works.
chmod 0755 "$tmp/$BINARY"
mv "$tmp/$BINARY" "$INSTALL_DIR/.$BINARY.new" || die "could not write to $INSTALL_DIR"
mv "$INSTALL_DIR/.$BINARY.new" "$INSTALL_DIR/$BINARY" || die "could not install to $INSTALL_DIR"

log ""
log "Installed $BINARY $version to $INSTALL_DIR/$BINARY"

case ":$PATH:" in
	*":$INSTALL_DIR:"*)
		log "Run 'stk init' in a repository to get started."
		;;
	*)
		log ""
		log "$INSTALL_DIR is not on your PATH. Add it:"
		log ""
		log "  export PATH=\"\$PATH:$INSTALL_DIR\""
		;;
esac
