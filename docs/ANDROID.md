# Android client

Sessions for Android is a client-only Tauri 2 application. It uses the same
React application as macOS and Windows, but it does not install `sessionsd`,
start agent CLIs, or host runners on the phone. Work stays on Sessions computers;
the Android app talks to its paired host, which can relay the host's approved fleet.
The Android platform bundle configuration excludes desktop runtime executables
from the APK; verify the built archive as well as its package identity.

## Product shape

- **Phone:** bottom navigation for Home, Sessions, Daily, Search, and More.
  Sessions opens the complete manager/child hierarchy; selecting a row pushes
  the existing Conversation / Terminal / Details view with a back action
  ([`frontend/src/App.tsx`](../frontend/src/App.tsx),
  [`frontend/src/components/MobileNav.tsx`](../frontend/src/components/MobileNav.tsx)).
- **Tablet and foldable:** the same layout progressively becomes the desktop
  rail + session navigator + active-session workspace. There is no separate
  tablet codebase
  ([`frontend/src/styles/globals.css`](../frontend/src/styles/globals.css)).
- **Account or pairing:** **Sign in** loads every reachable machine in the same
  Somewhere account and obtains a separate revocable credential from each host
  without an accept prompt. Without an account, run `sessions pair` on a host
  and use **Scan a pairing code**; pasting the link is the non-camera fallback
  ([`frontend/src/components/ConnectScreen.tsx`](../frontend/src/components/ConnectScreen.tsx),
  [`frontend/src/lib/hostedBootstrap.ts`](../frontend/src/lib/hostedBootstrap.ts)).
- **Host-owned setup:** the phone does not present the host's first-run
  onboarding. It inherits the connected computer's machine-level choices and
  shows host runtime, connection, provider, delegated-access, and AI
  settings as read-only; appearance and other device-local choices remain
  editable ([`frontend/src/App.tsx`](../frontend/src/App.tsx),
  [`frontend/src/components/SettingsView.tsx`](../frontend/src/components/SettingsView.tsx)).
- **Transport:** direct LAN or Tailscale connectivity to the paired host. That
  user-owned host can relay to machines it already has approval for; there is no
  Somewhere-hosted relay, hosted terminal stream, analytics connection, or model API call
  ([`docs/NETWORK_SECURITY.md`](NETWORK_SECURITY.md)).

Android release builds permit cleartext network traffic because a user-approved
LAN or Tailscale host can intentionally use an `http://` daemon endpoint.
Tailscale still encrypts its network transport; raw LAN mode is explicitly
unencrypted and should only be used on a trusted network. The app never scans
the public internet and only connects to a discovered, paired, or explicitly
entered endpoint
([`src-tauri/gen/android/app/build.gradle.kts`](../src-tauri/gen/android/app/build.gradle.kts),
[`src-tauri/gen/android/app/src/main/AndroidManifest.xml`](../src-tauri/gen/android/app/src/main/AndroidManifest.xml)).

Android performs multicast DNS discovery only while the connect screen is in
the foreground; it does not scan addresses or provide automatic Tailscale
discovery. QR pairing and foreground session control work without
FCM, while background FCM delivery is not represented as shipped
([`frontend/src/components/ConnectScreen.tsx`](../frontend/src/components/ConnectScreen.tsx)).

## Build a sideloadable test APK

Tauri's current Android prerequisites are JDK 17, the Android SDK platform and
build tools, platform tools, a side-by-side NDK, and the four Rust Android
targets ([Tauri Android prerequisites](https://v2.tauri.app/start/prerequisites/)).
On this repository's development Mac they are installed through Homebrew under
`/opt/homebrew`.

```sh
export JAVA_HOME=/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home
export ANDROID_HOME=/opt/homebrew/share/android-commandlinetools
export NDK_HOME="$ANDROID_HOME/ndk/29.0.14206865"

rustup target add \
  aarch64-linux-android armv7-linux-androideabi \
  i686-linux-android x86_64-linux-android

npx tauri android init --ci        # only when src-tauri/gen/android is absent
npx tauri android build --debug --apk --target aarch64 --ci
```

To build a local test app alongside the published Android preview, opt into the
separate package identity through a Gradle project property:

```sh
ORG_GRADLE_PROJECT_sessionsTestApp=true \
  npx tauri android build --debug --apk --target aarch64 --ci \
  --config '{"bundle":{"android":{"debugApplicationIdSuffix":".local"}}}'
```

That debug-only switch produces `tech.somewhere.sessions.local` with the
launcher label **Sessions Test**. Without it, debug builds remain
`tech.somewhere.sessions.debug` and release builds remain
`tech.somewhere.sessions`; the Android task and APK output path do not change.
Before installing, verify the built APK with `aapt dump badging` rather than
assuming that a package intended to be side by side has the expected identity.

The installable debug APK is:

```text
src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk
```

Every reviewed push to `main` also runs the **Android client preview**
workflow and retains the same sideloadable APK plus its SHA-256 checksum for
14 days. That artifact is a debug-signed test build, not a Play Store release.

Install it over USB debugging with:

```sh
"$ANDROID_HOME/platform-tools/adb" install -r \
  src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk
```

The debug APK is signed automatically by the Android development toolchain.
Release APKs are signed with the Sessions release keystore described below;
those credentials do not belong in this repository
([`src-tauri/gen/android/app/build.gradle.kts`](../src-tauri/gen/android/app/build.gradle.kts)).

## Release signing

Release builds read a Java properties file kept outside the repository:

```text
~/Library/Application Support/Sessions/signing/android-release.properties   (mode 600)
~/Library/Application Support/Sessions/signing/android-release.keystore     (mode 600)
```

```properties
storeFile=/Users/<you>/Library/Application Support/Sessions/signing/android-release.keystore
storePassword=...
keyAlias=sessions
keyPassword=...
```

`SESSIONS_ANDROID_SIGNING_PROPERTIES` points Gradle and the release script at
another file. When the file is absent, a release build is left unsigned and
`scripts/release-android.sh` refuses to continue. `.gitignore` excludes
`*.keystore`, `*.jks`, and `android-release.properties` in case a copy lands in
the tree.

The keystore was created with:

```sh
keytool -genkeypair -keystore android-release.keystore -storetype PKCS12 \
  -alias sessions -keyalg RSA -keysize 4096 -validity 36500 \
  -dname "CN=Sessions, O=Somewhere, C=US"
```

Back up both files somewhere private. Android accepts an update only when it is
signed by the same certificate as the installed app, so a lost keystore means
every phone must uninstall Sessions and pair again.

### Rotate the release key

Android can carry trust from an old key to a new one with APK Signature Scheme
v3 key rotation; a phone that has the old build installed then accepts an
update signed by the new key.

1. Generate the new keystore beside the old one with the command above (a new
   file name, for example `android-release-2.keystore`).
2. Build one release, then add the rotation proof with `apksigner`, signing
   with the old key first and the new key second:

   ```sh
   apksigner rotate --out sessions.lineage \
     --old-signer --ks android-release.keystore --ks-key-alias sessions \
     --new-signer --ks android-release-2.keystore --ks-key-alias sessions
   apksigner sign --ks android-release.keystore --ks-key-alias sessions \
     --next-signer --ks android-release-2.keystore --ks-key-alias sessions \
     --lineage sessions.lineage Sessions_X_android_arm64.apk
   ```

3. Install that APK over the current one and confirm it opens with paired
   machines intact.
4. Point `android-release.properties` at the new keystore. From then on normal
   releases are signed with the new key; keep the old keystore and
   `sessions.lineage` for reference.

Without rotation (for example after losing the old key), each phone uninstalls
Sessions, installs the new APK, and pairs again with `sessions pair`. Paired
credentials live only on the phone, and every computer keeps running.

## Release APK and in-app updates

```sh
scripts/release-android.sh --version X.Y.Z [--notes-file notes.md] [--base latest.json]
```

The script requires a clean worktree and versions that match
`tauri.conf.json`, `Cargo.toml`, and `frontend/package.json`. It runs
`tauri android build --target aarch64 --apk`, checks the APK's package name,
version, and `versionCode` with `aapt`, checks with `apksigner` that it is
signed by the release keystore, and writes into `release/out/vX.Y.Z/android/`:

- `Sessions_X.Y.Z_android_arm64.apk` and its `.sha256`
- `Sessions_X.Y.Z_android_arm64.apk.sig`, the Minisign signature made with the
  same updater key as the macOS app (`SESSIONS_UPDATER_KEY_PATH`)

It then renders `release/out/vX.Y.Z/latest.json` with an `android-aarch64`
platform, merged into the macOS manifest when one is present there or passed
with `--base`
([`scripts/release-android.sh`](../scripts/release-android.sh)).

`versionCode` is derived from the version so it always increases:
`MAJOR*1000000 + MINOR*10000 + PATCH`, so 0.2.48 is 20048.

The app checks the same release feed as the desktop app, hourly and 15 seconds
after launch. On Android it reads the `android-<arch>` entry; a release that has
no Android build yet counts as "no update". A newer version downloads in the
background and the updater plugin verifies its Minisign signature against the
key pinned in `tauri.conf.json` before Sessions keeps it. The card at the top of
**More** (and **Settings › Notifications & updates**) then offers **Install**.
Sessions writes the verified APK to its cache, checks that it is a newer build of
the same package signed by the same certificate, and opens Android's package
installer, which asks for confirmation
([`src-tauri/src/android_update.rs`](../src-tauri/src/android_update.rs),
[`UpdateInstallerPlugin.kt`](../src-tauri/gen/android/app/src/main/java/tech/somewhere/sessions/UpdateInstallerPlugin.kt)).

The first in-app update needs one permission: Android opens **Install unknown
apps** for Sessions; allow it, return, and tap **Install** again. Updating
replaces only the phone app; sessions and agents keep running on the computers.

## Install on a phone

1. Copy `Sessions_X.Y.Z_android_arm64.apk` to the phone (download it from the
   GitHub release, or `adb install` it over USB debugging).
2. Open it from Files or the browser's downloads. Android asks once to allow
   installs from that source (**Settings › Apps › Special app access › Install
   unknown apps**); allow it and install. Check the `.sha256` first if the file
   travelled through anything you do not trust.
3. Open Sessions and pair it with a computer as described below. Later
   versions arrive through the in-app update.

## Check the Android credential vault

The Android JVM unit tests exercise bounded encryption and failure-preserving
transactions with synthetic credentials. They do not exercise Android Keystore.
For OS-backed tests, `src-tauri/gen/android/vault-fixture` builds a small,
SDK-only instrumentation app using the exact production vault classes. It has
its own package and key alias, no network permission, and no real credentials.
Use only a disposable, task-owned emulator, not a user's phone:

```sh
cd src-tauri/gen/android/vault-fixture
../gradlew --offline --no-daemon assembleDebug

# Select the disposable emulator explicitly before installing anything.
SESSIONS_TEST_DEVICE=emulator-5580
"$ANDROID_HOME/platform-tools/adb" -s "$SESSIONS_TEST_DEVICE" install -r \
  build/outputs/apk/debug/sessions-vault-fixture-debug.apk
"$ANDROID_HOME/platform-tools/adb" -s "$SESSIONS_TEST_DEVICE" shell am instrument -w \
  -e phase seed tech.somewhere.sessions.vault.fixture/tech.somewhere.sessions.VaultInstrumentation
"$ANDROID_HOME/platform-tools/adb" -s "$SESSIONS_TEST_DEVICE" shell am force-stop \
  tech.somewhere.sessions.vault.fixture
"$ANDROID_HOME/platform-tools/adb" -s "$SESSIONS_TEST_DEVICE" shell am instrument -w \
  -e phase reopen tech.somewhere.sessions.vault.fixture/tech.somewhere.sessions.VaultInstrumentation
"$ANDROID_HOME/platform-tools/adb" -s "$SESSIONS_TEST_DEVICE" uninstall \
  tech.somewhere.sessions.vault.fixture
```

Both phases must print `passed`; the Android command's exit code alone does not
establish acceptance. The second phase tests fresh-process readback, tamper
refusal, verified rollback, and refusal to regenerate a lost key over existing
ciphertext. These tests prove the native vault backend, not the full
Rust/Kotlin/frontend migration journey or physical-device behavior. Run the
frontend credential migration regressions and build the native app separately.

## Pair a phone

On a Mac that already runs the current Sessions runtime and is on the same
trusted network as the phone:

Enable LAN access or sign into Tailscale on the host, run `sessions pair`, tap
**Scan a pairing code**, and scan the QR. Android asks for camera permission
only for that action. The paste-link field is the fallback when scanning is
inconvenient. A code is consumed once and expires in ten minutes by default;
the phone claims its credential and opens the host without another approval.
Revoke the phone later in Settings › Fleet with **Forget**, or with `sessions
devices`
([`runtime/cmd/sessions/pair.go`](../runtime/cmd/sessions/pair.go),
[`runtime/cmd/sessions/devices.go`](../runtime/cmd/sessions/devices.go)).

Alternatively, tap **Sign in**, enter the emailed Somewhere code, and choose a
machine from the account fleet. The phone registers an endpoint-free signing
identity and each same-account host verifies that identity through its own
owner-scoped directory token before issuing the phone a device credential.
Other accounts still require a pairing code or an accepted access request.

Without account sign-in, pair with one Mac and the phone also sees that Mac's approved Sessions fleet.
The paired Mac stays first, and its other saved machines appear in Fleet and the
all-machines inbox; opening or acting on one is relayed through the Mac with the
Mac's own independently revocable credential. An unreachable machine stays
visible as offline. The phone never receives the other machine's credential,
and each other machine can revoke the Mac exactly as before.
