#!/usr/bin/env bats
#
# ../install.sh, run as `curl ... | sh` runs it, its text on the input of sh: against GitHub, and with a fake uname and
# a fake curl (fakes/curl) serving releases built here for every platform. bats-support and bats-assert have to be
# where bats finds its libraries:
#
#   bats tests

# A test changes HOME and path for itself alone, and calls install_sh through run, which passes it the arguments
# shellcheck disable=SC2030,SC2031,SC2119,SC2120

bats_require_minimum_version 1.5.0
bats_load_library bats-support
bats_load_library bats-assert

# Releases v1.2.3 and v1.3.0, the latest, laid out as fakes/curl serves them. Each archive holds the files goreleaser
# puts in one, and its git-id prints the version and has the platform in a comment, so that a test can tell which
# archive it came from.
setup_file() {
  export FAKE_RELEASES="$BATS_FILE_TMPDIR/releases"
  local version platform build="$BATS_FILE_TMPDIR/build"
  for version in 1.2.3 1.3.0; do
    mkdir -p "$FAKE_RELEASES/v$version"
    for platform in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
      mkdir "$build"
      printf '#!/bin/sh\n# %s\necho "git-id v%s"\n' "$platform" "$version" > "$build/git-id"
      chmod +x "$build/git-id"
      touch "$build/LICENSE" "$build/README.md"
      tar -czf "$FAKE_RELEASES/v$version/git-id_${version}_$platform.tar.gz" -C "$build" LICENSE README.md git-id
      rm -r "$build"
    done
    (
      cd "$FAKE_RELEASES/v$version" || exit
      if command -v sha256sum > /dev/null; then sha256sum -- *.tar.gz; else shasum -a 256 -- *.tar.gz; fi \
        > "git-id_${version}_checksums.txt"
    )
  done
  echo v1.3.0 > "$FAKE_RELEASES/latest"
}

setup() {
  script="$BATS_TEST_DIRNAME/../install.sh"
  export HOME="$BATS_TEST_TMPDIR/home"
  tmp="$BATS_TEST_TMPDIR/tmp"
  mkdir -p "$HOME" "$tmp" "$BATS_TEST_TMPDIR/fakes"
  # The fakes first, then the system's commands alone, not the runner's
  path="$BATS_TEST_TMPDIR/fakes:$BATS_TEST_DIRNAME/fakes:/usr/bin:/bin:/usr/sbin:/sbin"
  platform Linux x86_64
}

# Has uname report a system for -s and a machine for -m: platform <system> <machine>
platform() {
  # shellcheck disable=SC2016 # expanded by the fake
  printf '#!/bin/sh\ncase $1 in -s) echo %s ;; -m) echo %s ;; esac\n' "$1" "$2" > "$BATS_TEST_TMPDIR/fakes/uname"
  chmod +x "$BATS_TEST_TMPDIR/fakes/uname"
}

# Runs the script as `curl ... | sh` runs it, in an environment of only HOME, PATH ($path), a TMPDIR of its own, what
# fakes/curl reads, and the NAME=value arguments; the other arguments go to the script:
# install_sh [NAME=value ...] [argument ...]
install_sh() {
  local vars=()
  while [[ ${1-} == *=* ]]; do
    vars+=("$1")
    shift
  done
  env -i HOME="$HOME" PATH="$path" TMPDIR="$tmp" FAKE_RELEASES="$FAKE_RELEASES" FAKE_CALLS="$BATS_TEST_TMPDIR/calls" \
    ${vars[@]+"${vars[@]}"} sh -s -- "$@" < "$script"
}

# The calls fakes/curl logged, a line each
calls() {
  cat "$BATS_TEST_TMPDIR/calls" 2> /dev/null || true
}

# The version ~/.local/bin/git-id prints, and the platform of the archive it came from
installed() {
  echo "$("$HOME/.local/bin/git-id" version) $(sed -n 's/^# //p' "$HOME/.local/bin/git-id")"
}

# What a run left behind: anything in its TMPDIR, and anything in ~/.local/bin but git-id
leftovers() {
  find "$tmp" "$HOME/.local/bin" -mindepth 1 ! -path "$HOME/.local/bin/git-id" 2> /dev/null || true
}

#  With GitHub
#-----------------------------------------------
@test "it installs the latest release from GitHub, which runs here" {
  path=/usr/bin:/bin:/usr/sbin:/sbin
  # An empty curlrc of its own: when HOME has none, curl looks for one beyond it
  : > "$HOME/.curlrc"
  run -0 install_sh
  assert_line --regexp '^git-id_[0-9.]+_(darwin|linux)_(amd64|arm64)\.tar\.gz: OK$'
  local version
  version="$("$HOME/.local/bin/git-id" version)"
  assert_line "Installed $version in $HOME/.local/bin"
}

@test "a curlrc that follows redirects itself changes nothing" {
  path=/usr/bin:/bin:/usr/sbin:/sbin
  echo location > "$HOME/.curlrc"
  run -0 install_sh
  local version
  version="$("$HOME/.local/bin/git-id" version)"
  assert_line "Installed $version in $HOME/.local/bin"
}

#  With the fakes
#-----------------------------------------------
@test "without a version, it installs the latest release, for the platform uname reports" {
  local system machine expected wrong=''
  while read -r system machine expected; do
    export HOME="$BATS_TEST_TMPDIR/$system-$machine"
    mkdir "$HOME"
    platform "$system" "$machine"
    run -0 install_sh
    [ "$(installed)" = "git-id v1.3.0 $expected" ] || wrong+="$system $machine: $(installed)"$'\n'
  done << 'EOF'
Darwin x86_64 darwin_amd64
Darwin arm64 darwin_arm64
Linux x86_64 linux_amd64
Linux aarch64 linux_arm64
EOF
  assert_equal "$wrong" ''
}

@test "on another system or machine, it fails before downloading anything" {
  platform FreeBSD amd64
  run -1 install_sh
  assert_line 'git-id: no release for FreeBSD; see https://github.com/creasty/git-id#install'
  # Git Bash, on Windows
  platform MINGW64_NT-10.0-26100 x86_64
  run -1 install_sh
  assert_line 'git-id: no release for MINGW64_NT-10.0-26100; see https://github.com/creasty/git-id#install'
  platform Linux armv7l
  run -1 install_sh
  assert_line 'git-id: no release for armv7l; see https://github.com/creasty/git-id#install'
  assert_equal "$(calls)" ''
  [ ! -e "$HOME/.local" ]
}

@test "a version, with or without its v, installs that release" {
  run -0 install_sh 1.2.3
  assert_equal "$(installed)" 'git-id v1.2.3 linux_amd64'
  rm -r "$HOME/.local"
  run -0 install_sh v1.2.3
  assert_equal "$(installed)" 'git-id v1.2.3 linux_amd64'
}

@test "the version installed is left as it is, with nothing downloaded" {
  run -0 install_sh 1.2.3
  rm "$BATS_TEST_TMPDIR/calls"
  run -0 install_sh 1.2.3
  assert_line "git-id v1.2.3 is already in $HOME/.local/bin"
  assert_equal "$(calls)" ''

  # Without a version, it takes the request that finds the latest
  run -0 install_sh
  rm "$BATS_TEST_TMPDIR/calls"
  run -0 install_sh
  assert_line "git-id v1.3.0 is already in $HOME/.local/bin"
  assert_equal "$(calls)" 'curl -fsSLI --retry 3 -o /dev/null -w %{url_effective} https://github.com/creasty/git-id/releases/latest'
}

@test "another version installed is replaced, with nothing left beside it" {
  run -0 install_sh 1.2.3
  run -0 install_sh
  assert_line "Installed git-id v1.3.0 in $HOME/.local/bin"
  assert_equal "$(installed)" 'git-id v1.3.0 linux_amd64'
  assert_equal "$(leftovers)" ''
}

@test "an archive that fails its checksum, or has none, is not installed" {
  run -0 install_sh 1.2.3
  cp -R "$FAKE_RELEASES" "$BATS_TEST_TMPDIR/releases"
  FAKE_RELEASES="$BATS_TEST_TMPDIR/releases"
  local release="$FAKE_RELEASES/v1.3.0"

  # Another platform's archive in its place
  cp "$release/git-id_1.3.0_linux_arm64.tar.gz" "$release/git-id_1.3.0_linux_amd64.tar.gz"
  run -1 install_sh
  assert_line 'git-id_1.3.0_linux_amd64.tar.gz: FAILED'
  assert_equal "$(installed)" 'git-id v1.2.3 linux_amd64'
  assert_equal "$(leftovers)" ''

  # Missing from the checksums
  grep -v linux_amd64 "$release/git-id_1.3.0_checksums.txt" > "$BATS_TEST_TMPDIR/checksums.txt"
  mv "$BATS_TEST_TMPDIR/checksums.txt" "$release/git-id_1.3.0_checksums.txt"
  run -1 install_sh
  assert_line 'git-id: no checksum for git-id_1.3.0_linux_amd64.tar.gz'
  assert_equal "$(installed)" 'git-id v1.2.3 linux_amd64'
  assert_equal "$(leftovers)" ''
}

@test "a version without a release fails, naming what it couldn't download" {
  run -1 install_sh 9.9.9
  assert_line 'git-id: cannot download https://github.com/creasty/git-id/releases/download/v9.9.9/git-id_9.9.9_linux_amd64.tar.gz'
  [ ! -e "$HOME/.local/bin/git-id" ]
  assert_equal "$(leftovers)" ''
}

@test "it writes nothing to stdout, which Claude Code gives the model from a SessionStart hook" {
  run -0 --separate-stderr install_sh
  assert_equal "$output" ''
  [ -n "$stderr" ]
}

@test "with ~/.local/bin off PATH, it says to add it" {
  run -0 install_sh
  assert_line "Add $HOME/.local/bin to PATH to run git id"
}

@test "in a Claude Code session or a GitHub Actions job, it puts ~/.local/bin on PATH through their files" {
  local env_file="$BATS_TEST_TMPDIR/claude.env" github_path="$BATS_TEST_TMPDIR/github_path"
  run -0 install_sh CLAUDE_ENV_FILE="$env_file" GITHUB_PATH="$github_path"
  # shellcheck disable=SC2016 # expanded by the shell that runs the file
  assert_equal "$(cat "$env_file")" 'export PATH="$PATH:'"$HOME/.local/bin"'"'
  assert_equal "$(cat "$github_path")" "$HOME/.local/bin"
  refute_output --partial 'to PATH to run'

  # Claude Code runs the file before each of Claude's shell commands
  run -0 env -i PATH=/usr/bin:/bin sh -c ". '$env_file' && command -v git-id"
  assert_output "$HOME/.local/bin/git-id"
}

@test "with ~/.local/bin on PATH, it writes neither file" {
  path="$HOME/.local/bin:$path"
  run -0 install_sh CLAUDE_ENV_FILE="$BATS_TEST_TMPDIR/claude.env" GITHUB_PATH="$BATS_TEST_TMPDIR/github_path"
  [ ! -e "$BATS_TEST_TMPDIR/claude.env" ]
  [ ! -e "$BATS_TEST_TMPDIR/github_path" ]
  refute_output --partial PATH
}

@test "it names another git-id that comes first on PATH" {
  mkdir "$BATS_TEST_TMPDIR/other"
  printf '#!/bin/sh\necho "git-id v0.0.9"\n' > "$BATS_TEST_TMPDIR/other/git-id"
  chmod +x "$BATS_TEST_TMPDIR/other/git-id"
  path="$BATS_TEST_TMPDIR/other:$path"
  run -0 install_sh
  assert_line "PATH finds $BATS_TEST_TMPDIR/other/git-id first: git-id v0.0.9"
}

@test "cut short before its last line, it does nothing" {
  local whole="$script" n changed=''
  script="$BATS_TEST_TMPDIR/cut.sh"
  for ((n = 1; n < $(wc -l < "$whole"); n++)); do
    head -n "$n" "$whole" > "$script"
    install_sh > /dev/null 2>&1 || true
    [ ! -e "$HOME/.local" ] || changed+="$n "
  done
  assert_equal "$changed" ''
  assert_equal "$(calls)" ''
}
