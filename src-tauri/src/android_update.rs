//! Android in-app updates. The updater plugin checks the same signed feed as
//! macOS under the `android-<arch>` target and verifies the APK's Minisign
//! signature against the pinned key while downloading. Its own install is a
//! no-op on mobile, so the verified bytes are written to the app cache and
//! handed to Android's package installer, which asks the person to confirm.
use serde::{Deserialize, Serialize};
use tauri::{
    plugin::{Builder, PluginHandle, TauriPlugin},
    AppHandle, Manager,
};

struct UpdateInstaller(PluginHandle<tauri::Wry>);

#[derive(Serialize)]
struct InstallRequest {
    path: String,
}

#[derive(Deserialize)]
struct InstallReply {
    #[allow(dead_code)]
    started: bool,
}

/// The release-feed platform key for this build, e.g. `android-aarch64`.
pub(crate) fn updater_target() -> String {
    format!("android-{}", std::env::consts::ARCH)
}

pub(crate) fn init() -> TauriPlugin<tauri::Wry> {
    Builder::new("update-installer")
        .setup(|app, api| {
            let handle =
                api.register_android_plugin("tech.somewhere.sessions", "UpdateInstallerPlugin")?;
            app.manage(UpdateInstaller(handle));
            Ok(())
        })
        .build()
}

/// Writes the verified APK to cache/updates/ and opens Android's installer.
/// Returning Ok means the installer is showing; Android replaces the app if
/// the person confirms. Runners and sessions live on the computers and are
/// never affected.
pub(crate) fn install(app: &AppHandle, version: &str, bytes: &[u8]) -> Result<(), String> {
    let installer = app.try_state::<UpdateInstaller>().ok_or_else(|| {
        "The Android update installer is unavailable. Install the APK from the release page.".to_string()
    })?;
    let directory = app
        .path()
        .app_cache_dir()
        .map_err(|_| "Sessions cannot find its cache directory.".to_string())?
        .join("updates");
    // Only the newest download is ever kept.
    let _ = std::fs::remove_dir_all(&directory);
    std::fs::create_dir_all(&directory)
        .map_err(|error| format!("Sessions could not prepare the update: {error}"))?;
    let path = directory.join(format!("Sessions_{version}_android.apk"));
    std::fs::write(&path, bytes)
        .map_err(|error| format!("Sessions could not save the update: {error}"))?;
    installer
        .0
        .run_mobile_plugin::<InstallReply>(
            "installApk",
            InstallRequest {
                path: path.to_string_lossy().into_owned(),
            },
        )
        .map(|_| ())
        .map_err(|error| match error {
            tauri::plugin::mobile::PluginInvokeError::InvokeRejected(rejection) => rejection
                .message
                .unwrap_or_else(|| "Android did not start the install.".to_string()),
            other => format!("Android did not start the install: {other}"),
        })
}
