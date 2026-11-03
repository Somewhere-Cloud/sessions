// The verified Sessions.app update this window downloaded, held by the native
// shell rather than the viewer so that quitting can install it.
//
// The viewer still finds the update with the updater plugin's check. Download
// and install go through these commands: the plugin verifies the Minisign
// signature while downloading, and its install is the same bundle swap the
// "Restart" button has always used. Installing replaces only the app bundle;
// sessionsd and every runner are launchd-owned and keep running, and the next
// launch moves the background service to the new runtime.

#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
const CHECK_FOR_UPDATES_MENU_ID: &str = "check-for-updates";
#[cfg_attr(not(target_os = "macos"), allow(dead_code))]
const CHECK_FOR_UPDATES_EVENT: &str = "sessions://check-for-updates";
// Installing on quit happens while the process is exiting. A bundle swap that
// needs an administrator prompt cannot be shown then; the plugin would wait on
// the main thread forever. Give the install a bounded window and exit anyway:
// in that case the old app was never moved, so nothing is half-replaced.
#[cfg_attr(not(desktop), allow(dead_code))]
const INSTALL_ON_QUIT_TIMEOUT: Duration = Duration::from_secs(60);

#[derive(Default)]
struct StagedUpdate {
    staged: Mutex<Option<(tauri_plugin_updater::Update, Vec<u8>)>>,
}

#[derive(Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct NativeUpdateProgress {
    downloaded_bytes: u64,
    total_bytes: Option<u64>,
}

fn staged_version(app: &AppHandle) -> Option<String> {
    app.state::<StagedUpdate>()
        .staged
        .lock()
        .ok()
        .and_then(|staged| staged.as_ref().map(|(update, _)| update.version.clone()))
}

// Downloads and verifies `version` without installing it. Nothing on disk
// changes, so this is safe while sessions work.
#[tauri::command]
async fn native_update_download(
    app: AppHandle,
    version: String,
    on_progress: tauri::ipc::Channel<NativeUpdateProgress>,
) -> Result<(), String> {
    use tauri_plugin_updater::UpdaterExt;
    if staged_version(&app).as_deref() == Some(version.as_str()) {
        return Ok(());
    }
    let update = app
        .updater()
        .map_err(|error| error.to_string())?
        .check()
        .await
        .map_err(|error| error.to_string())?
        .ok_or_else(|| "Sessions is already up to date".to_string())?;
    if update.version != version {
        return Err(format!(
            "the release feed now offers Sessions {}; check for updates again",
            update.version
        ));
    }
    let mut downloaded: u64 = 0;
    let bytes = update
        .download(
            |chunk, total| {
                downloaded += chunk as u64;
                let _ = on_progress.send(NativeUpdateProgress {
                    downloaded_bytes: downloaded,
                    total_bytes: total,
                });
            },
            || {},
        )
        .await
        .map_err(|error| error.to_string())?;
    let state = app.state::<StagedUpdate>();
    let mut staged = state
        .staged
        .lock()
        .map_err(|_| "update state is unavailable".to_string())?;
    *staged = Some((update, bytes));
    Ok(())
}

// Installs the downloaded update now and relaunches only this window.
#[tauri::command]
fn native_update_install(app: AppHandle) -> Result<(), String> {
    let state = app.state::<StagedUpdate>();
    let taken = state
        .staged
        .lock()
        .map_err(|_| "update state is unavailable".to_string())?
        .take();
    let Some((update, bytes)) = taken else {
        return Err("Download the update first".to_string());
    };
    if let Err(error) = update.install(&bytes) {
        // The bundle was not replaced; keep the download for another try.
        if let Ok(mut staged) = state.staged.lock() {
            *staged = Some((update, bytes));
        }
        return Err(error.to_string());
    }
    app.restart();
}

// Quitting with a downloaded update installs it, so the next launch is the new
// version. Only macOS: there the install is a bundle swap that leaves this
// process alone. Windows installs through its own installer on Restart.
#[cfg_attr(not(desktop), allow(dead_code))]
fn install_staged_update_on_quit(app: &AppHandle) {
    if !cfg!(target_os = "macos") {
        return;
    }
    let Some(state) = app.try_state::<StagedUpdate>() else {
        return;
    };
    let taken = match state.staged.lock() {
        Ok(mut staged) => staged.take(),
        Err(_) => None,
    };
    let Some((update, bytes)) = taken else {
        return;
    };
    let version = update.version.clone();
    let (done, finished) = std::sync::mpsc::channel();
    thread::spawn(move || {
        let _ = done.send(update.install(&bytes).map_err(|error| error.to_string()));
    });
    match finished.recv_timeout(INSTALL_ON_QUIT_TIMEOUT) {
        Ok(Ok(())) => log::info!("Sessions {version} installed on quit"),
        Ok(Err(error)) => log::warn!("Sessions {version} was not installed on quit: {error}"),
        Err(_) => log::warn!("Sessions {version} was not installed on quit: it needed more than {INSTALL_ON_QUIT_TIMEOUT:?}"),
    }
}

// The macOS app menu gains "Check for Updates…" under About. It asks the
// viewer to check, which shows the result in the window.
#[cfg(target_os = "macos")]
fn install_app_menu(app: &tauri::App) -> tauri::Result<()> {
    let menu = Menu::default(app.handle())?;
    if let Some(app_menu) = menu.items()?.first().and_then(|item| item.as_submenu().cloned()) {
        let check = MenuItem::with_id(
            app,
            CHECK_FOR_UPDATES_MENU_ID,
            "Check for Updates…",
            true,
            None::<&str>,
        )?;
        app_menu.insert(&check, 1)?;
    }
    app.set_menu(menu)?;
    app.on_menu_event(|app, event| {
        if event.id().as_ref() == CHECK_FOR_UPDATES_MENU_ID {
            request_update_check(app);
        }
    });
    Ok(())
}

#[cfg(target_os = "macos")]
fn request_update_check(app: &AppHandle) {
    use tauri::Emitter;
    if let Err(error) = open_window(app, main_window_spec()) {
        log::warn!("show the main window to check for updates: {error}");
    }
    if let Err(error) = app.emit_to("main", CHECK_FOR_UPDATES_EVENT, ()) {
        log::warn!("ask the viewer to check for updates: {error}");
    }
}
