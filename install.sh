#!/bin/sh
# Installs onesie from a GitHub release:
#
#   curl -fsSL https://raw.githubusercontent.com/frodi-karlsson/onesie/main/install.sh | sh
#
# ONESIE_VERSION        the release to install, such as 1.2.3. Defaults to the latest release.
# ONESIE_INSTALL_DIR    where to put the binary. Defaults to $HOME/.local/bin.
# ONESIE_RELEASE_BASE_URL  where the archives and checksums.txt live, for a mirror or a test.
#
# The archive is checked against the release's checksums.txt. When gh is installed and logged in,
# it is also checked against the build provenance the release workflow attested.
set -eu

repo="frodi-karlsson/onesie"

# Everything runs from main, so a download cut short by the pipe runs nothing.
main() {
	if [ "$#" -ne 0 ]; then
		echo "usage: install.sh, configured through ONESIE_VERSION, ONESIE_INSTALL_DIR and ONESIE_RELEASE_BASE_URL" >&2
		exit 2
	fi

	case "$(uname -s)" in
	Darwin) os="darwin" ;;
	Linux) os="linux" ;;
	*) die "the installer supports macOS and Linux only. Use go install on other systems." ;;
	esac

	case "$(uname -m)" in
	x86_64 | amd64) arch="amd64" ;;
	arm64 | aarch64) arch="arm64" ;;
	*) die "the installer supports amd64 and arm64 only." ;;
	esac

	need curl
	need tar
	need awk
	if command -v sha256sum >/dev/null 2>&1; then
		hasher="sha256sum"
	elif command -v shasum >/dev/null 2>&1; then
		hasher="shasum"
	else
		die "sha256sum or shasum is needed to verify the download."
	fi

	version="${ONESIE_VERSION:-}"
	version="${version#v}"

	if [ -n "${ONESIE_RELEASE_BASE_URL:-}" ]; then
		base="${ONESIE_RELEASE_BASE_URL%/}"
	elif [ -n "$version" ]; then
		base="https://github.com/$repo/releases/download/v$version"
	else
		base="https://github.com/$repo/releases/latest/download"
	fi

	install_dir="${ONESIE_INSTALL_DIR:-$HOME/.local/bin}"

	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' HUP INT TERM

	fetch "$base/checksums.txt" "$tmp/checksums.txt"

	suffix="_${os}_${arch}.tar.gz"
	if [ -n "$version" ]; then
		asset="onesie_$version$suffix"
	else
		asset="$(awk -v want="$suffix" '
			index($2, "onesie_") == 1 && length($2) > length(want) &&
			substr($2, length($2) - length(want) + 1) == want { print $2 }
		' "$tmp/checksums.txt")"
		case "$asset" in
		"") die "checksums.txt at $base lists no archive for $os $arch." ;;
		*"
"*) die "checksums.txt at $base lists more than one archive for $os $arch." ;;
		esac
	fi

	case "$asset" in
	*[!A-Za-z0-9._+-]*) die "checksums.txt at $base names an unexpected archive: $asset" ;;
	esac

	want="$(awk -v name="$asset" '$2 == name { print $1 }' "$tmp/checksums.txt")"
	[ -n "$want" ] || die "checksums.txt at $base has no entry for $asset."

	echo "Downloading $asset"
	fetch "$base/$asset" "$tmp/$asset"

	got="$(sha256 "$tmp/$asset")"
	[ "$got" = "$want" ] || die "the sha256 of $asset is $got, but checksums.txt says $want."
	echo "Verified the sha256 of $asset"

	if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
		gh attestation verify "$tmp/$asset" --repo "$repo" >/dev/null ||
			die "gh attestation verify found no build provenance from $repo that matches $asset."
		echo "Verified the build provenance of $asset"
	else
		echo "Skipped the build provenance check, which needs gh installed and logged in"
	fi

	tar -xzf "$tmp/$asset" -C "$tmp" onesie

	mkdir -p "$install_dir" ||
		die "could not create $install_dir. Set ONESIE_INSTALL_DIR to a directory you can write to."
	# A copy and a rename, so a running onesie is never overwritten in place.
	cp "$tmp/onesie" "$install_dir/.onesie.$$" ||
		die "could not write to $install_dir. Set ONESIE_INSTALL_DIR to a directory you can write to."
	chmod 755 "$install_dir/.onesie.$$"
	mv -f "$install_dir/.onesie.$$" "$install_dir/onesie"

	installed="${asset#onesie_}"
	echo "Installed onesie ${installed%"$suffix"} to $install_dir/onesie"

	case ":$PATH:" in
	*":$install_dir:"*) ;;
	*)
		echo "warning: $install_dir is not on PATH. Add it in your shell profile, then open a new shell:" >&2
		echo "  export PATH=\"$install_dir:\$PATH\"" >&2
		;;
	esac
}

fetch() {
	# file is allowed, so a test can point ONESIE_RELEASE_BASE_URL at a local directory.
	curl -fsSL --proto '=https,file' --proto-redir '=https' -o "$2" "$1" ||
		die "could not download $1"
}

sha256() {
	if [ "$hasher" = "shasum" ]; then
		shasum -a 256 "$1"
	else
		sha256sum "$1"
	fi | awk '{ print $1 }'
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is needed to install onesie."
}

die() {
	echo "onesie install: $*" >&2
	exit 1
}

main "$@"
