#!/usr/bin/env bash
# Cuts a release: checks the tree and the tag, bumps the plugin manifests,
# commits, tags, and prints the push. It pushes nothing.
#
#   scripts/release.sh v0.2.0
#   DRY_RUN=1 scripts/release.sh v0.2.0
#
# It runs on the bash 3.2 macOS ships, so it uses no associative arrays and
# no mapfile.
set -euo pipefail

if [ "$#" -ne 1 ]; then
	echo "usage: scripts/release.sh TAG, for example scripts/release.sh v0.2.0" >&2
	exit 2
fi

tag=$1
read -r -a prep <<<"${RELEASEPREP:-go run ./cmd/releaseprep}"
make=${MAKE:-make}

case ${DRY_RUN:-} in
'' | 0) dry_run=0 ;;
1) dry_run=1 ;;
*)
	echo "release: DRY_RUN is 1 for a dry run, or 0 or empty for a release, not $DRY_RUN" >&2
	exit 2
	;;
esac

# Make passes its single letter flags as the first word of MAKEFLAGS, unless
# that word is a variable or a long option.
make_flags=${MAKEFLAGS:-}
case ${make_flags%% *} in
-* | *=*) ;;
*[ntqi]*)
	echo "release: make was run with -n, -t, -q or -i, which would skip the checks or ignore their failures. Preview with DRY_RUN=1 instead" >&2
	exit 2
	;;
esac

cd "$(git rev-parse --show-toplevel)"

manifests=(
	.claude-plugin/plugin.json
	.codex-plugin/plugin.json
	.claude-plugin/marketplace.json
	.agents/plugins/marketplace.json
)

jq() {
	if [ -n "${JQ:-}" ]; then
		"$JQ" "$@"
	else
		go tool gojq "$@"
	fi
}

# The release workflow's own checks, line for line. A test holds them to
# .github/workflows/release.yml.
check_manifests() {
	test "$(jq -r .version .claude-plugin/plugin.json)" = "${tag#v}"
	test "$(jq -r .version .codex-plugin/plugin.json)" = "${tag#v}"
	test "$(jq -r '.plugins[0].source.ref' .claude-plugin/marketplace.json)" = "$tag"
	test "$(jq -r '.plugins[0].source.ref' .agents/plugins/marketplace.json)" = "$tag"
}

fail() {
	echo "release: $*" >&2
	exit 1
}

plural() {
	if [ "$1" -eq 1 ]; then
		echo "1 $2"
	else
		echo "$1 $2s"
	fi
}

has_tag() {
	git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null
}

before=$(git rev-parse HEAD)
had_tag=0
if has_tag; then
	had_tag=1
fi
finished=0

# Undoes whatever the run changed, working only from the values saved above,
# so it is right whichever step the run stopped at.
cleanup() {
	status=$?
	if [ "$finished" -eq 1 ] || [ "$dry_run" -eq 1 ]; then
		exit "$status"
	fi

	undid=0
	if [ "$had_tag" -eq 0 ] && has_tag; then
		git tag -d "$tag" >/dev/null
		undid=1
	fi
	if [ "$(git rev-parse HEAD)" != "$before" ]; then
		git reset --quiet --keep "$before"
		undid=1
	fi
	if ! git diff --quiet "$before" -- "${manifests[@]}"; then
		git checkout "$before" -- "${manifests[@]}"
		undid=1
	fi
	if [ "$undid" -eq 1 ]; then
		echo "release: stopped, and put the tree, HEAD and tags back as they were" >&2
	fi
	exit "$status"
}

if [ -n "$(git status --porcelain)" ]; then
	fail "the tree has uncommitted changes. Commit or stash them first"
fi

# Armed only once the tree is known to be clean, so a refusal never touches
# work in progress.
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

branch=$(git symbolic-ref --quiet --short HEAD || true)
if [ "$branch" != main ]; then
	fail "release runs on main, and this is ${branch:-a detached HEAD}"
fi

# No tags, so nothing the fetch brings in is mistaken for the run's own.
# ls-remote reads the remote's tags below.
git fetch --quiet --no-tags origin
upstream=$(git rev-parse origin/main)
if [ "$before" != "$upstream" ]; then
	ahead=$(git rev-list --count origin/main..HEAD)
	behind=$(git rev-list --count HEAD..origin/main)
	if [ "$behind" -eq 0 ]; then
		fail "HEAD is $(plural "$ahead" commit) ahead of origin/main, which would ship unreviewed commits"
	elif [ "$ahead" -eq 0 ]; then
		fail "HEAD is $(plural "$behind" commit) behind origin/main, so the push would fail after the tag exists. Pull first"
	else
		fail "HEAD is $(plural "$ahead" commit) ahead of and $(plural "$behind" commit) behind origin/main"
	fi
fi

{
	git tag --list 'v*'
	git ls-remote --tags origin 'refs/tags/v*'
} | "${prep[@]}" check-tag "$tag"

MAKEFLAGS='' MFLAGS='' "$make" check
MAKEFLAGS='' MFLAGS='' "$make" skills-check
if [ "$(git rev-parse HEAD)" != "$before" ]; then
	fail "make check or make skills-check moved HEAD"
fi
if [ -n "$(git status --porcelain)" ]; then
	fail "make check or make skills-check changed the tree"
fi

prerelease=0
if "${prep[@]}" prerelease "$tag" 2>/dev/null; then
	prerelease=1
fi

release_commit=$before

if [ "$prerelease" -eq 1 ]; then
	echo "$tag is a prerelease, so the manifests stay as they are"
else
	planned=$("${prep[@]}" bump -dry-run "$tag")
	if ! printf '%s\n' "$planned" | grep -q '^would set '; then
		fail "the manifests already name $tag, so there is no release commit to make. Pick the next version"
	fi

	if [ "$dry_run" -eq 1 ]; then
		printf '%s\n' "$planned"
		declare -f check_manifests | sed -n -e 's/;$//' -e 's/^ *\(test .*\)$/\1/p' |
			while IFS= read -r check; do
				case $check in
				*'#v}"') expected=${tag#v} ;;
				*) expected=$tag ;;
				esac
				echo "would run: $check, which expects $expected"
			done
		echo "would commit chore: release $tag"
	else
		"${prep[@]}" bump "$tag"
		check_manifests
		git add -- "${manifests[@]}"
		git commit -S --quiet -m "chore: release $tag"
		release_commit=$(git rev-parse HEAD)
	fi
fi

push="git push --atomic origin main $tag"

if [ "$dry_run" -eq 1 ]; then
	echo "would tag $tag"
	echo "$push"
	exit 0
fi

git tag -s "$tag" -m "$tag" "$release_commit"
echo "tagged $tag. Push the branch and the tag together with:"
echo "$push"
finished=1
