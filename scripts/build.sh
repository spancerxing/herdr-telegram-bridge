#!/bin/sh
# Builds bin/herdr-tg. Called by `herdr plugin install`'s [[build]] step and by
# `make build`.
#
# Herdr runs build steps with a thin environment, so the Go toolchain is
# searched for rather than assumed to be on PATH. The ~/go-sdk location is
# where this repo was developed (see docs/DESIGN.md for why Go was installed
# there rather than via a package manager).

set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$root"

find_go() {
  if command -v go >/dev/null 2>&1; then
    command -v go
    return 0
  fi
  for candidate in "${HOME:-}/go-sdk/bin/go" /usr/local/go/bin/go /opt/homebrew/bin/go; do
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

if ! GO="$(find_go)"; then
  cat >&2 <<'MSG'
error: no Go toolchain found.

This plugin compiles from source and has no prebuilt binary yet. Install Go
and re-run, or run `make build` from a shell that has it:

  brew install go

If Go lives somewhere unusual, point the build at it explicitly:

  GO=/path/to/go/bin/go make build
MSG
  exit 1
fi

echo "building with $("$GO" version)"
mkdir -p bin

# CGO is off so the result is a static binary that runs anywhere, which
# matters because the plugin is spawned by Herdr rather than by a shell that
# inherited a toolchain's environment.
CGO_ENABLED=0 "$GO" build -trimpath -ldflags "-s -w" -o bin/herdr-tg ./cmd/herdr-tg

echo "built bin/herdr-tg"
