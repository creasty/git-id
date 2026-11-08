#!/bin/sh
# Installs git id from its GitHub releases into ~/.local/bin, on macOS and Linux:
#
#   curl -fsSL https://raw.githubusercontent.com/creasty/git-id/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/creasty/git-id/main/install.sh | sh -s -- 0.1.0
#
# Without a version it installs the latest release. A version already installed is left as it is,
# so this can run at the start of every agent session or CI job.
set -eu

repo=creasty/git-id
bin_dir="$HOME/.local/bin"

fail() {
  echo "git-id: $*" >&2
  exit 1
}

# All in one function, called on the last line, so a download cut short runs nothing.
main() {
  # Nothing on stdout: Claude Code hands the stdout of a SessionStart hook to the model.
  exec 1>&2

  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) fail "no release for $(uname -s); see https://github.com/$repo#install" ;;
  esac
  case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) fail "no release for $(uname -m); see https://github.com/$repo#install" ;;
  esac

  if [ $# -gt 0 ]; then
    tag="v${1#v}"
  else
    # Read from where the latest release redirects, not from the API, which rate-limits anonymous calls.
    url=$(curl -fsSLI --retry 3 -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")
    tag=${url##*/}
  fi
  version=${tag#v}

  if [ -x "$bin_dir/git-id" ] && [ "$("$bin_dir/git-id" version)" = "git-id $tag" ]; then
    echo "git-id $tag is already in $bin_dir"
  else
    archive="git-id_${version}_${os}_${arch}.tar.gz"
    base="https://github.com/$repo/releases/download/$tag"
    tmp=$(mktemp -d)
    staged="$bin_dir/.git-id.$$"
    trap 'rm -rf "$tmp" "$staged"' EXIT

    echo "Downloading git-id $tag for $os/$arch"
    curl -fsSL --retry 3 -o "$tmp/$archive" "$base/$archive" || fail "cannot download $base/$archive"
    curl -fsSL --retry 3 -o "$tmp/checksums.txt" "$base/git-id_${version}_checksums.txt"
    (
      cd "$tmp"
      grep "  $archive\$" checksums.txt > expected.txt
      if command -v sha256sum > /dev/null; then sha256sum -c expected.txt; else shasum -a 256 -c expected.txt; fi
    )
    tar -xzf "$tmp/$archive" -C "$tmp" git-id

    # Renamed into place, so a session starting meanwhile never runs a half-written file.
    mkdir -p "$bin_dir"
    install -m 0755 "$tmp/git-id" "$staged"
    mv -f "$staged" "$bin_dir/git-id"
    echo "Installed git-id $tag in $bin_dir"
  fi

  case ":$PATH:" in
    *":$bin_dir:"*) ;;
    *)
      # Claude Code runs CLAUDE_ENV_FILE before each shell command Claude runs later in the session;
      # GitHub Actions puts the directories in GITHUB_PATH on PATH for the later steps of the job.
      if [ -n "${CLAUDE_ENV_FILE:-}" ]; then echo "export PATH=\"\$PATH:$bin_dir\"" >> "$CLAUDE_ENV_FILE"; fi
      if [ -n "${GITHUB_PATH:-}" ]; then echo "$bin_dir" >> "$GITHUB_PATH"; fi
      if [ -z "${CLAUDE_ENV_FILE:-}${GITHUB_PATH:-}" ]; then echo "Add $bin_dir to PATH to run git id"; fi
      PATH="$PATH:$bin_dir"
      ;;
  esac

  # Another install, such as Homebrew's, may come first.
  found=$(command -v git-id)
  if [ "$found" != "$bin_dir/git-id" ]; then echo "PATH finds $found first: $("$found" version)"; fi
}

main "$@"
