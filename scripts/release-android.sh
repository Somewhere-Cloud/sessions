#!/usr/bin/env bash
set -euo pipefail

# Builds the signed Sessions for Android release APK (arm64 only), verifies its
# APK signature and package identity, signs it with the same Minisign updater
# key as the macOS app, and renders the `android-aarch64` entry of the updater
# manifest. It publishes nothing. See docs/ANDROID.md.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KEY_PATH="${SESSIONS_UPDATER_KEY_PATH:-$HOME/.config/sessions/sessions-updater.key}"
SIGNING_PROPERTIES="${SESSIONS_ANDROID_SIGNING_PROPERTIES:-$HOME/Library/Application Support/Sessions/signing/android-release.properties}"
EXPECTED_PUBLIC_KEY="$ROOT/release/updater.pub"
PACKAGE_ID="tech.somewhere.sessions"
VERSION=""
NOTES_FILE=""
BASE_MANIFEST=""
DRY_RUN=0

usage() {
  cat >&2 <<'EOF'
Usage: scripts/release-android.sh --version X.Y.Z [--notes-file PATH] [--base latest.json] [--dry-run]

Builds release/out/vX.Y.Z/android/Sessions_X.Y.Z_android_arm64.apk with its
.sha256 and Minisign .sig, and writes release/out/vX.Y.Z/latest.json with an
android-aarch64 platform. --base merges into an existing same-version manifest
(the macOS release's latest.json); by default release/out/vX.Y.Z/latest.json is
used as the base when it already exists.

Required local secrets (never in the repository):
  SESSIONS_ANDROID_SIGNING_PROPERTIES  APK signing keystore properties
      (default ~/Library/Application Support/Sessions/signing/android-release.properties)
  SESSIONS_UPDATER_KEY_PATH            Minisign updater key
      (default ~/.config/sessions/sessions-updater.key)
EOF
}

while (($#)); do
  case "$1" in
    --version) VERSION="${2:-}"; shift 2 ;;
    --notes-file) NOTES_FILE="${2:-}"; shift 2 ;;
    --base) BASE_MANIFEST="${2:-}"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; echo "error: unknown argument $1" >&2; exit 2 ;;
  esac
done

if [[ ! "$VERSION" =~ ^([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  usage
  echo "error: --version must be MAJOR.MINOR.PATCH without a leading v" >&2
  exit 2
fi
# Must match sessionsVersionCode() in src-tauri/gen/android/app/build.gradle.kts.
VERSION_CODE=$((10#${BASH_REMATCH[1]} * 1000000 + 10#${BASH_REMATCH[2]} * 10000 + 10#${BASH_REMATCH[3]}))
if ((10#${BASH_REMATCH[2]} >= 100 || 10#${BASH_REMATCH[3]} >= 10000)); then
  echo "error: $VERSION cannot be encoded as a monotonic Android versionCode" >&2
  exit 2
fi
if [[ -z "$NOTES_FILE" && -f "$ROOT/release/notes-v${VERSION}.md" ]]; then
  NOTES_FILE="$ROOT/release/notes-v${VERSION}.md"
fi
if [[ -n "$NOTES_FILE" && ! -f "$NOTES_FILE" ]]; then
  echo "error: --notes-file must name an existing file" >&2
  exit 2
fi

CONFIG_VERSION="$(node -p "require('$ROOT/src-tauri/tauri.conf.json').version")"
FRONTEND_VERSION="$(node -p "require('$ROOT/frontend/package.json').version")"
CARGO_VERSION="$(sed -n 's/^version = "\([^"]*\)"/\1/p' "$ROOT/src-tauri/Cargo.toml" | head -1)"
for pair in "tauri.conf.json:$CONFIG_VERSION" "frontend/package.json:$FRONTEND_VERSION" "Cargo.toml:$CARGO_VERSION"; do
  if [[ "${pair#*:}" != "$VERSION" ]]; then
    echo "error: ${pair%%:*} is ${pair#*:}, expected $VERSION" >&2
    exit 1
  fi
done

OUTPUT_DIR="$ROOT/release/out/v${VERSION}"
ANDROID_DIR="$OUTPUT_DIR/android"
APK_NAME="Sessions_${VERSION}_android_arm64.apk"
APK_URL="https://github.com/Somewhere-Cloud/sessions/releases/download/v${VERSION}/${APK_NAME}"
BUILT_APK="$ROOT/src-tauri/gen/android/app/build/outputs/apk/universal/release/app-universal-release.apk"
if [[ -z "$BASE_MANIFEST" && -f "$OUTPUT_DIR/latest.json" ]]; then
  BASE_MANIFEST="$OUTPUT_DIR/latest.json"
fi

if ((DRY_RUN)); then
  printf 'release version: %s (versionCode %s)\n' "$VERSION" "$VERSION_CODE"
  printf 'updater target: android-aarch64\n'
  printf 'immutable artifact: %s\n' "$APK_URL"
  printf 'manifest output: %s/latest.json%s\n' "$OUTPUT_DIR" "${BASE_MANIFEST:+ (merged into $BASE_MANIFEST)}"
  printf 'dry run: no build, signing, or publication performed\n'
  exit 0
fi

if [[ ! -f "$SIGNING_PROPERTIES" ]]; then
  echo "error: Android release signing properties are missing at $SIGNING_PROPERTIES" >&2
  echo "       create the keystore as described in docs/ANDROID.md (Release signing)" >&2
  exit 1
fi
if [[ ! -f "$KEY_PATH" || ! -f "$KEY_PATH.pub" ]]; then
  echo "error: updater signing keypair is missing at $KEY_PATH{,.pub}" >&2
  exit 1
fi
if [[ "$(tr -d '\r\n' < "$KEY_PATH.pub")" != "$(tr -d '\r\n' < "$EXPECTED_PUBLIC_KEY")" ]]; then
  echo "error: updater public key does not match the key pinned in the app" >&2
  exit 1
fi
if [[ -e "$ANDROID_DIR" ]]; then
  echo "error: $ANDROID_DIR already exists; remove it to rebuild this version" >&2
  exit 1
fi

# The APK is built from the working tree; release only reviewed sources.
release_tree_state="$(git -C "$ROOT" status --porcelain=v1 --untracked-files=all)"
if [[ -n "$release_tree_state" ]]; then
  echo "error: release builds require a clean reviewed worktree (tracked and untracked)" >&2
  printf '%s\n' "$release_tree_state" >&2
  exit 1
fi

export ANDROID_HOME="${ANDROID_HOME:-/opt/homebrew/share/android-commandlinetools}"
export JAVA_HOME="${JAVA_HOME:-/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home}"
if [[ -z "${NDK_HOME:-}" ]]; then
  NDK_HOME="$(find "$ANDROID_HOME/ndk" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort -V | tail -1)"
  export NDK_HOME
fi
BUILD_TOOLS="$(find "$ANDROID_HOME/build-tools" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort -V | tail -1)"
for tool in "$JAVA_HOME/bin/keytool" "$BUILD_TOOLS/apksigner" "$BUILD_TOOLS/aapt" "$NDK_HOME/source.properties"; do
  if [[ ! -e "$tool" ]]; then
    echo "error: Android toolchain is incomplete: $tool is missing (docs/ANDROID.md)" >&2
    exit 1
  fi
done
export PATH="$JAVA_HOME/bin:$PATH"

if [[ "${ORG_GRADLE_PROJECT_sessionsWebDebug:-}" == "true" ]]; then
  echo "error: unset ORG_GRADLE_PROJECT_sessionsWebDebug; a release APK never allows WebView debugging" >&2
  exit 1
fi
npm --prefix "$ROOT" exec tauri android build -- --target aarch64 --apk --ci

if [[ ! -f "$BUILT_APK" ]]; then
  echo "error: the signed release APK was not produced at $BUILT_APK" >&2
  echo "       an unsigned build means Gradle did not read $SIGNING_PROPERTIES" >&2
  exit 1
fi

# The APK must carry this exact identity and be signed by the release keystore.
badging="$("$BUILD_TOOLS/aapt" dump badging "$BUILT_APK")"
expected_identity="package: name='$PACKAGE_ID' versionCode='$VERSION_CODE' versionName='$VERSION'"
if [[ "$(printf '%s\n' "$badging" | head -1)" != "$expected_identity"* ]]; then
  echo "error: APK identity is not $expected_identity" >&2
  printf '%s\n' "$badging" | head -1 >&2
  exit 1
fi
if printf '%s\n' "$badging" | grep -q "^native-code:.*'x86"; then
  echo "error: the release APK must contain only arm64-v8a native code" >&2
  exit 1
fi
store_file="$(sed -n 's/^storeFile=//p' "$SIGNING_PROPERTIES")"
store_password="$(sed -n 's/^storePassword=//p' "$SIGNING_PROPERTIES")"
key_alias="$(sed -n 's/^keyAlias=//p' "$SIGNING_PROPERTIES")"
expected_cert="$(keytool -list -keystore "$store_file" -storepass "$store_password" -alias "$key_alias" \
  | sed -n 's/^Certificate fingerprint (SHA-256): //p' | tr -d ':' | tr '[:upper:]' '[:lower:]')"
apk_cert="$("$BUILD_TOOLS/apksigner" verify --print-certs "$BUILT_APK" \
  | sed -n 's/^Signer #1 certificate SHA-256 digest: //p')"
if [[ -z "$expected_cert" || "$apk_cert" != "$expected_cert" ]]; then
  echo "error: the APK is not signed by the release keystore ($apk_cert != $expected_cert)" >&2
  exit 1
fi
"$BUILD_TOOLS/apksigner" verify --min-sdk-version 24 "$BUILT_APK"

mkdir -p "$OUTPUT_DIR"
mkdir -m 0700 "$ANDROID_DIR"
cp "$BUILT_APK" "$ANDROID_DIR/$APK_NAME"
(cd "$ANDROID_DIR" && shasum -a 256 "$APK_NAME" > "$APK_NAME.sha256" && shasum -a 256 -c "$APK_NAME.sha256")

# Minisign signature for the in-app updater, with the integrity-pinned signer
# the macOS release uses (scripts/sign-release-assets.sh).
SIGNER_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sessions-android-signer.XXXXXX")"
trap 'rm -rf "$SIGNER_DIR"' EXIT
node "$ROOT/scripts/stage-updater-signer.mjs" "$SIGNER_DIR/tool"
TAURI_SIGNING_PRIVATE_KEY_PASSWORD="${TAURI_SIGNING_PRIVATE_KEY_PASSWORD:-}" \
  node "$ROOT/scripts/sign-updater-artifact.mjs" "$SIGNER_DIR/tool" "$KEY_PATH" "$ANDROID_DIR/$APK_NAME"
node "$ROOT/scripts/verify-updater-signature.mjs" --public-key "$EXPECTED_PUBLIC_KEY" \
  --artifact "$ANDROID_DIR/$APK_NAME" --signature "$ANDROID_DIR/$APK_NAME.sig"

manifest_args=(--version "$VERSION" --artifact "$ANDROID_DIR/$APK_NAME" --url "$APK_URL"
  --target android-aarch64 --output "$OUTPUT_DIR/latest.json")
if [[ -n "$BASE_MANIFEST" ]]; then
  cp "$BASE_MANIFEST" "$ANDROID_DIR/base-latest.json"
  manifest_args+=(--base "$ANDROID_DIR/base-latest.json")
fi
if [[ -n "$NOTES_FILE" ]]; then
  manifest_args+=(--notes-file "$NOTES_FILE")
fi
node "$ROOT/scripts/render-updater-manifest.mjs" "${manifest_args[@]}"
rm -f "$ANDROID_DIR/base-latest.json"

printf 'Signed Android release verified.\n'
printf '  APK:      %s\n' "$ANDROID_DIR/$APK_NAME"
printf '  SHA-256:  %s\n' "$(cut -d' ' -f1 "$ANDROID_DIR/$APK_NAME.sha256")"
printf '  cert:     %s\n' "$apk_cert"
printf '  manifest: %s/latest.json (android-aarch64)\n' "$OUTPUT_DIR"
printf 'Upload %s and %s.sig to the v%s GitHub release, then publish latest.json.\n' "$APK_NAME" "$APK_NAME" "$VERSION"
