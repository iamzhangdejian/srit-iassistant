#!/usr/bin/env bash
# Build the anydoc static archive that the `anydoc` build tag links.
#
# The archive is a Rust build artifact (~30 MB), so it is neither committed nor
# downloaded: this script builds it from third_party/anydoc-go, which pins the
# published anydoc crate, and drops it where cgo looks for it.
#
# Usage:
#   scripts/build-anydoc-lib.sh              # host platform
#   TARGET=aarch64-unknown-linux-musl scripts/build-anydoc-lib.sh
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
crate_dir="$repo_root/third_party/anydoc-go"

if ! command -v cargo >/dev/null 2>&1; then
  echo "error: cargo not found. Install Rust (https://rustup.rs) to build the anydoc archive." >&2
  exit 1
fi

# The Rust target triple decides the archive; the directory name mirrors what
# the cgo LDFLAGS in third_party/anydoc-go expect for that platform.
target=${TARGET:-$(rustc -vV | sed -n 's/^host: //p')}
case "$target" in
  x86_64-apple-darwin) lib_dir=darwin_amd64 ;;
  aarch64-apple-darwin) lib_dir=darwin_arm64 ;;
  x86_64-pc-windows-msvc) lib_dir=windows_amd64 ;;
  # The Go side links with `-L${SRCDIR}/lib/windows_amd64 -lanydoc_go`, and the
  # Windows desktop bundle is cross-built from macOS with a MinGW toolchain
  # (see scripts/oem-build-lite.sh), so the GNU triple is the one that ships.
  # It produces a .a archive, unlike the MSVC triple's .lib.
  x86_64-pc-windows-gnu) lib_dir=windows_amd64 ;;
  x86_64-unknown-linux-gnu) lib_dir=linux_amd64_gnu ;;
  aarch64-unknown-linux-gnu) lib_dir=linux_arm64_gnu ;;
  x86_64-unknown-linux-musl) lib_dir=linux_amd64_musl ;;
  aarch64-unknown-linux-musl) lib_dir=linux_arm64_musl ;;
  *)
    echo "error: unsupported target '$target'." >&2
    echo "Supported: {x86_64,aarch64}-{apple-darwin,unknown-linux-{gnu,musl}}, x86_64-pc-windows-{msvc,gnu}" >&2
    exit 1
    ;;
esac

case "$target" in
  *windows-msvc) lib_name=anydoc_go.lib ;;
  *) lib_name=libanydoc_go.a ;;
esac

# The crate version to patch is read from Cargo.toml rather than repeated here,
# so an upgrade is a one-line change to the `anydoc = "=X.Y.Z"` pin.
anydoc_version=$(sed -n 's/^anydoc = "=\([0-9][^"]*\)".*/\1/p' "$crate_dir/Cargo.toml" | head -1)
if [ -z "$anydoc_version" ]; then
  echo "error: no pinned 'anydoc = \"=X.Y.Z\"' dependency in $crate_dir/Cargo.toml" >&2
  exit 1
fi

# Include a patch revision so changes to our patches invalidate existing copies
# even when the upstream anydoc version stays the same.
anydoc_patch_id="$anydoc_version-zip9-v1"

# Copy the pinned version, expose the official serializer, and upgrade zip to
# the fixed major version (upstream anydoc 0.2.4 still requires zip ^8.6.0).
prepare_patched_anydoc() {
  local dest="$crate_dir/patched-anydoc"
  # The marker records the upstream version and patch revision: after a bump the
  # copy left by the previous build is the wrong crate, and reusing it would
  # fail deep inside cargo's patch resolution instead of here. Cargo.toml is
  # required too: a truncated leftover with only the marker would otherwise
  # skip the copy and fail with "failed to read .../Cargo.toml".
  if [ -f "$dest/.weknora-patched" ] && [ "$(cat "$dest/.weknora-patched")" = "$anydoc_patch_id" ] && [ -f "$dest/Cargo.toml" ]; then
    return
  fi

  local src=""
  local cargo_home="${CARGO_HOME:-$HOME/.cargo}"
  src=$(find "$cargo_home/registry/src" -maxdepth 2 -type d -name "anydoc-$anydoc_version" 2>/dev/null | head -1 || true)

  if [ -z "$src" ]; then
    local tarball="$crate_dir/.anydoc-$anydoc_version.crate"
    echo "Fetching anydoc $anydoc_version to patch document_to_markdown into the public API"
    if ! curl -fsSL "https://rsproxy.cn/api/v1/crates/anydoc/$anydoc_version/download" -o "$tarball"; then
      curl -fsSL "https://static.crates.io/crates/anydoc/anydoc-$anydoc_version.crate" -o "$tarball"
    fi
    local unpack="$crate_dir/.anydoc-unpack"
    rm -rf "$unpack"
    mkdir -p "$unpack"
    tar -xzf "$tarball" -C "$unpack"
    src=$(find "$unpack" -maxdepth 1 -type d -name "anydoc-$anydoc_version" | head -1)
    if [ -z "$src" ]; then
      echo "error: unpacked anydoc-$anydoc_version crate is missing" >&2
      exit 1
    fi
  fi

  rm -rf "$dest"
  cp -R "$src" "$dest"
  if ! grep -q '^use render::markdown::document_to_markdown;$' "$dest/src/lib.rs"; then
    echo "error: anydoc $anydoc_version lib.rs no longer has the expected document_to_markdown import" >&2
    exit 1
  fi
  sed -i.bak 's/^use render::markdown::document_to_markdown;/pub use render::markdown::document_to_markdown;/' "$dest/src/lib.rs"
  rm -f "$dest/src/lib.rs.bak"
  # RUSTSEC-2026-0367/0368/0369 have no fix in zip 8.x. Only change zip's
  # version, preserving its deflate feature and disabled default features.
  # Fail on upstream manifest changes rather than silently leaving zip unpatched.
  if ! awk '
    /^\[/ { in_zip = ($0 == "[dependencies.zip]") }
    in_zip && /^version = / {
      if ($0 != "version = \"8.6.0\"") exit 1
      $0 = "version = \"9\""
      patched++
    }
    { print }
    END { if (patched != 1) exit 1 }
  ' "$dest/Cargo.toml" > "$dest/Cargo.toml.tmp"; then
    echo "error: anydoc $anydoc_version no longer has the expected zip 8.6.0 dependency" >&2
    exit 1
  fi
  mv "$dest/Cargo.toml.tmp" "$dest/Cargo.toml"
  printf '%s\n' "$anydoc_patch_id" > "$dest/.weknora-patched"
  rm -rf "$crate_dir/.anydoc-unpack" "$crate_dir/.anydoc-$anydoc_version.crate"
}

# version.go is what the parser reports as `anydoc_version` on every parsed
# document, so a bump that misses it would mislabel the whole knowledge base.
go_version=$(sed -n 's/^const Version = "\([^"]*\)".*/\1/p' "$crate_dir/version.go" | head -1)
if [ "$go_version" != "$anydoc_version" ]; then
  echo "error: version.go says '$go_version' but Cargo.toml pins anydoc '$anydoc_version'." >&2
  echo "Bump both: the Go constant is the version WeKnora records for parsed documents." >&2
  exit 1
fi

echo "Building anydoc $anydoc_version archive for $target"
prepare_patched_anydoc

# A windows-gnu target needs an explicit linker: rustc does not know that
# x86_64-w64-mingw32-gcc is the MinGW driver, and the Go side cross-compiles
# with the very same toolchain, so both halves must agree on it.
if [[ "$target" == *windows-gnu ]]; then
  # Two hosts build this archive:
  #   - Windows native (MSYS2 UCRT64): the driver is plain `gcc`, there is no
  #     x86_64-w64-mingw32-gcc name, and the toolchain must be UCRT because
  #     duckdb's prebuilt static libs are (see scripts/oem-build-lite.sh).
  #   - macOS/Linux cross-build: the driver carries the target triple prefix.
  # So resolve the driver from $CC first, then from the two conventional names.
  mingw_cc="${CC:-}"
  if [[ -z "$mingw_cc" ]]; then
    if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
      mingw_cc=x86_64-w64-mingw32-gcc
    elif command -v gcc >/dev/null 2>&1; then
      mingw_cc=gcc
    fi
  fi
  if [[ -z "$mingw_cc" ]] || ! command -v "$mingw_cc" >/dev/null 2>&1; then
    echo "error: no MinGW-w64 gcc found. Install one (brew install mingw-w64 on macOS," >&2
    echo "       pacman -S mingw-w64-ucrt-x86_64-gcc in MSYS2 UCRT64 on Windows) or set CC." >&2
    exit 1
  fi
  if ! rustup target list --installed 2>/dev/null | grep -qx "$target"; then
    echo "error: Rust target '$target' is not installed. Run: rustup target add $target" >&2
    exit 1
  fi
  export CARGO_TARGET_X86_64_PC_WINDOWS_GNU_LINKER="$mingw_cc"
  echo "Using MinGW linker: $mingw_cc"
fi

# --locked: build exactly the dependency versions in the committed Cargo.lock.
# That lockfile is what pins the patched pdf-inspector/lopdf, so a silent
# resolver drift must fail the build rather than ship an unaudited tree.
cargo build --release --locked --manifest-path "$crate_dir/Cargo.toml" --target "$target"

dest="$crate_dir/lib/$lib_dir"
mkdir -p "$dest"
cp "$crate_dir/target/$target/release/$lib_name" "$dest/$lib_name"

echo "Wrote $dest/$lib_name"
echo "Build WeKnora with the archive: go build -tags anydoc ./cmd/server"
if [[ "$target" == *windows-gnu ]]; then
  echo "Cross-build the desktop app with: TARGET=windows/amd64 bash scripts/oem-build-lite.sh"
fi
