#!/usr/bin/env bash
# build-dist.sh — the single source of truth for Orbit release artifacts.
#
# Usage:
#   scripts/build-dist.sh <goos> <goarch> [goarm]   build and package one target
#   scripts/build-dist.sh --all                     build the whole matrix + checksums
#   scripts/build-dist.sh --checksums               (re)write checksums for existing packages
#
# Environment overrides:
#   VERSION    version to stamp and to name the packages with
#              (default: git describe --tags --dirty, then v0.0.0-dev)
#   DIST_DIR   output directory (default: <repo>/dist)
#   ZIP_BIN    zip binary used for Windows packages (default: zip)
#
# Packages are dist/orbit_<version>_<goos>_<goarch>[v<goarm>].tar.gz, or .zip on
# Windows. Every archive contains orbit (orbit.exe on Windows), LICENSE and
# README.md at its root. dist/orbit_<version>_checksums.txt holds the sha256 of
# every package, one line per file, so "cd dist && sha256sum -c <file>" works.
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "$BASH_SOURCE")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

DIST_DIR="$(printenv DIST_DIR || true)"
if [ -z "$DIST_DIR" ]; then DIST_DIR="$REPO_ROOT/dist"; fi
ZIP_BIN="$(printenv ZIP_BIN || true)"
if [ -z "$ZIP_BIN" ]; then ZIP_BIN="zip"; fi
VERSION_ENV="$(printenv VERSION || true)"

log() { printf '%s\n' "$*"; }
die() { printf 'build-dist: error: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'USAGE'
usage: scripts/build-dist.sh <goos> <goarch> [goarm]
       scripts/build-dist.sh --all
       scripts/build-dist.sh --checksums

  <goos> <goarch> [goarm]  build and package a single target
  --all                    build the full matrix, then write the checksums file
  --checksums              only (re)write dist/orbit_<version>_checksums.txt
  --clean                  remove packaged artifacts and staging leftovers, then exit

Environment overrides: VERSION, DIST_DIR, ZIP_BIN.
USAGE
}

resolve_version() {
  local v="$VERSION_ENV"
  if [ -z "$v" ]; then
    v="$(git -C "$REPO_ROOT" describe --tags --dirty 2>/dev/null || true)"
  fi
  if [ -z "$v" ]; then
    v="v0.0.0-dev"
  fi
  # VERSION is user input; keep it safe for a file name.
  printf '%s' "$(printf '%s' "$v" | tr '/ ' '--')"
}

VERSION="$(resolve_version)"
COMMIT="$(git -C "$REPO_ROOT" rev-parse --short HEAD 2>/dev/null || echo none)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
MODULE="$(sed -n 's/^module[[:space:]]*//p' "$REPO_ROOT/go.mod")"
if [ -z "$MODULE" ]; then die "cannot read the module path from go.mod"; fi
LDFLAGS="-X $MODULE/internal/version.Version=$VERSION -X $MODULE/internal/version.Commit=$COMMIT -X $MODULE/internal/version.Date=$DATE"

binary_name() {
  if [ "$1" = windows ]; then printf 'orbit.exe'; else printf 'orbit'; fi
}

artifact_name() { # goos goarch goarm
  local goos="$1" goarch="$2" goarm="$3" arch ext
  arch="$goarch"
  if [ -n "$goarm" ]; then arch="$goarch""v""$goarm"; fi
  ext="tar.gz"
  if [ "$goos" = windows ]; then ext="zip"; fi
  printf 'orbit_%s_%s_%s.%s' "$VERSION" "$goos" "$arch" "$ext"
}

build_one() { # goos goarch goarm
  local goos="$1" goarch="$2" goarm="$3" label bin stage out pkg
  command -v go >/dev/null 2>&1 || die "go is not in PATH (source ../.toolchains/env.sh)"
  bin="$(binary_name "$goos")"
  label="$goos/$goarch"
  if [ -n "$goarm" ]; then label="$label/armv$goarm"; fi

  mkdir -p "$DIST_DIR"
  stage="$DIST_DIR/.stage/$goos-$goarch"
  if [ -n "$goarm" ]; then stage="$DIST_DIR/.stage/$goos-$goarch-armv$goarm"; fi
  rm -rf "$stage"
  mkdir -p "$stage"

  log "building $label (version $VERSION)"
  if [ -n "$goarm" ]; then
    ( cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" GOARM="$goarm" \
        go build -trimpath -ldflags "$LDFLAGS" -o "$stage/$bin" ./cmd/orbit )
  else
    ( cd "$REPO_ROOT" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -trimpath -ldflags "$LDFLAGS" -o "$stage/$bin" ./cmd/orbit )
  fi
  cp "$REPO_ROOT/LICENSE" "$REPO_ROOT/README.md" "$stage/"

  out="$DIST_DIR/$(artifact_name "$goos" "$goarch" "$goarm")"
  # Package inside the staging dir first, then replace the previous artifact.
  # A failure halfway (missing zip, full disk) must not delete the package that
  # was already there, and must not leave staging directories behind.
  pkg="$(basename "$out")"
  if [ "$goos" = windows ]; then
    if ! command -v "$ZIP_BIN" >/dev/null 2>&1; then
      rm -rf "$stage"; rmdir "$DIST_DIR/.stage" 2>/dev/null || true
      die "'$ZIP_BIN' is required to package Windows builds; install zip (e.g. apt-get install zip)"
    fi
    if ! ( cd "$stage" && "$ZIP_BIN" -q -X "$pkg" "$bin" LICENSE README.md ); then
      rm -rf "$stage"; rmdir "$DIST_DIR/.stage" 2>/dev/null || true
      die "packaging $label failed"
    fi
  else
    if ! tar -C "$stage" -czf "$stage/$pkg" "$bin" LICENSE README.md; then
      rm -rf "$stage"; rmdir "$DIST_DIR/.stage" 2>/dev/null || true
      die "packaging $label failed"
    fi
  fi
  remove_stale_target "$goos" "$goarch" "$goarm"
  mv -f "$stage/$pkg" "$out"
  rm -rf "$stage"
  rmdir "$DIST_DIR/.stage" 2>/dev/null || true   # drop the staging dir once empty
  log "  -> $(basename "$out") ($(du -h "$out" | cut -f1 | tr -d '[:space:]'))"
}

# stale_pattern lists the file-name shapes this script produces. Anything matching
# one of them is ours to delete; anything else in dist/ belongs to the operator.
stale_pattern() {
  local goos="$1" goarch="$2" goarm="$3"
  local suffix=""
  if [ "$goarch" = arm ] && [ -n "$goarm" ]; then suffix="v$goarm"; fi
  printf '%s' "orbit_*_"$goos"_"$goarch""$suffix".*"
}

# remove_stale_target drops previous packages of the same target regardless of the
# version they were named with, so dist/ never mixes versions after a rebuild.
remove_stale_target() {
  local goos="$1" goarch="$2" goarm="$3" pat
  pat="$(stale_pattern "$goos" "$goarch" "$goarm")"
  # shellcheck disable=SC2086 # deliberate glob expansion
  rm -f $DIST_DIR/$pat
}

# prune_dist removes every artifact this script could have produced, plus staging
# leftovers. Used by --all (deterministic output) and --clean.
prune_dist() {
  local f
  for f in "$DIST_DIR"/orbit_*.tar.gz "$DIST_DIR"/orbit_*.zip "$DIST_DIR"/orbit_*_checksums.txt; do
    if [ -f "$f" ]; then rm -f "$f"; fi
  done
  rm -rf "$DIST_DIR/.stage" "$DIST_DIR/.packages.tmp"
}

write_checksums() {
  local name out tmp count p
  command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required to write checksums"
  mkdir -p "$DIST_DIR"
  name="orbit_""$VERSION""_checksums.txt"
  out="$DIST_DIR/$name"
  tmp="$DIST_DIR/.packages.tmp"
  : > "$tmp"
  for p in "$DIST_DIR"/orbit_"$VERSION"_*.tar.gz "$DIST_DIR"/orbit_"$VERSION"_*.zip; do
    if [ -f "$p" ]; then printf '%s\n' "$(basename "$p")" >> "$tmp"; fi
  done
  count="$(wc -l < "$tmp" | tr -d '[:space:]')"
  if [ "$count" -eq 0 ]; then rm -f "$tmp"; die "no packages for $VERSION in $DIST_DIR"; fi
  # Every package this script produced must be covered. A mismatch means another
  # version is still sitting in dist/; fail loudly instead of publishing a partial
  # checksum file that would look fine to sha256sum -c.
  local total=0 p
  for p in "$DIST_DIR"/orbit_*.tar.gz "$DIST_DIR"/orbit_*.zip; do
    if [ -f "$p" ]; then total=$((total + 1)); fi
  done
  if [ "$total" -ne "$count" ]; then
    rm -f "$tmp"
    die "dist/ holds $total packages but only $count match $VERSION; run --clean or remove the stale ones first"
  fi
  LC_ALL=C sort "$tmp" -o "$tmp"
  rm -f "$out"
  ( cd "$DIST_DIR" && while IFS= read -r p; do sha256sum "$p"; done < "$tmp" > "$name" )
  rm -f "$tmp"
  log "checksums -> $name ($count entries)"
}

build_all() {
  local line goos goarch goarm count=0
  # Deterministic output: a stale package from another version would otherwise
  # survive and silently be left out of the checksums file.
  prune_dist
  while IFS= read -r line; do
    if [ -z "$line" ]; then continue; fi
    goos=""; goarch=""; goarm=""
    # shellcheck disable=SC2086 # matrix entries are intentional word lists
    set -- $line
    goos="$1"; goarch="$2"
    if [ "$#" -ge 3 ]; then goarm="$3"; fi
    build_one "$goos" "$goarch" "$goarm"
    count=$((count + 1))
  done <<'MATRIX'
linux amd64
linux arm64
linux arm 7
linux 386
darwin amd64
darwin arm64
windows amd64
windows arm64
freebsd amd64
MATRIX
  write_checksums
  log "built $count targets; packages are in $DIST_DIR"
}

if [ "$#" -eq 0 ]; then
  usage
  exit 2
fi

case "$1" in
  --all)
    [ "$#" -eq 1 ] || die "--all takes no arguments"
    build_all
    ;;
  --checksums)
    [ "$#" -eq 1 ] || die "--checksums takes no arguments"
    write_checksums
    ;;
  --clean)
    [ "$#" -eq 1 ] || die "--clean takes no arguments"
    prune_dist
    log "removed packaged artifacts from $DIST_DIR"
    ;;
  -h|--help)
    usage
    ;;
  -*)
    die "unknown option: $1"
    ;;
  *)
    [ "$#" -ge 2 ] && [ "$#" -le 3 ] || die "expected <goos> <goarch> [goarm]; run with --help"
    goarm=""
    if [ "$#" -eq 3 ]; then goarm="$3"; fi
    build_one "$1" "$2" "$goarm"
    ;;
esac
