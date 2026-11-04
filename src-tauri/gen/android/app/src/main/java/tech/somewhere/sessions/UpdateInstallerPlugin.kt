package tech.somewhere.sessions

import android.app.Activity
import android.content.Intent
import android.content.pm.PackageInfo
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.core.content.FileProvider
import app.tauri.annotation.Command
import app.tauri.annotation.InvokeArg
import app.tauri.annotation.TauriPlugin
import app.tauri.plugin.Invoke
import app.tauri.plugin.JSObject
import app.tauri.plugin.Plugin
import java.io.File

@InvokeArg
class UpdateInstallArgs { lateinit var path: String }

// Hands an already-verified Sessions APK to Android's package installer.
//
// Rust downloads the APK with the updater plugin, which verifies the Minisign
// signature against the key pinned in tauri.conf.json, and writes it to
// cache/updates/. This plugin only checks that the file is a newer build of
// this same package, signed with this same certificate, and asks Android to
// install it. Android shows its own confirmation; the person decides.
@TauriPlugin
class UpdateInstallerPlugin(private val activity: Activity) : Plugin(activity) {
  @Command
  fun installApk(invoke: Invoke) {
    val args = try { invoke.parseArgs(UpdateInstallArgs::class.java) } catch (_: Exception) {
      invoke.reject("Sessions could not prepare the update.")
      return
    }
    val apk = File(args.path)
    val updates = File(activity.cacheDir, "updates").canonicalFile
    if (!apk.isFile || apk.canonicalFile.parentFile != updates) {
      invoke.reject("The downloaded update is missing. Download it again.")
      return
    }
    val problem = mismatch(apk)
    if (problem != null) {
      invoke.reject(problem)
      return
    }
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && !activity.packageManager.canRequestPackageInstalls()) {
      // First update only: Android requires "Install unknown apps" for this app.
      activity.startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${activity.packageName}")))
      invoke.reject("Allow Sessions to install apps (Install unknown apps), then tap Install again. The download is kept.")
      return
    }
    val uri = FileProvider.getUriForFile(activity, "${activity.packageName}.fileprovider", apk)
    val intent = Intent(Intent.ACTION_VIEW)
      .setDataAndType(uri, "application/vnd.android.package-archive")
      .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK)
    try {
      activity.startActivity(intent)
      invoke.resolve(JSObject().put("started", true))
    } catch (_: Exception) {
      invoke.reject("Android's package installer did not open. Install the APK from the release page instead.")
    }
  }

  private fun mismatch(apk: File): String? {
    val manager = activity.packageManager
    @Suppress("DEPRECATION")
    val flags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) PackageManager.GET_SIGNING_CERTIFICATES else PackageManager.GET_SIGNATURES
    val candidate = manager.getPackageArchiveInfo(apk.path, flags)
      ?: return "The downloaded update is not a valid Android package. Download it again."
    val installed = manager.getPackageInfo(activity.packageName, flags)
    if (candidate.packageName != activity.packageName) {
      return "The downloaded update is for ${candidate.packageName}, not this app. Sessions is unchanged."
    }
    if (versionCode(candidate) <= versionCode(installed)) {
      return "The downloaded update is not newer than this app. Sessions is unchanged."
    }
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P && certificates(candidate) != certificates(installed)) {
      return "The downloaded update is signed by a different key than this app. Reinstall Sessions from the release page."
    }
    return null
  }

  @Suppress("DEPRECATION")
  private fun versionCode(info: PackageInfo): Long =
    if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.P) info.longVersionCode else info.versionCode.toLong()

  private fun certificates(info: PackageInfo): Set<String> {
    if (Build.VERSION.SDK_INT < Build.VERSION_CODES.P) return emptySet()
    val signing = info.signingInfo ?: return emptySet()
    val signatures = if (signing.hasMultipleSigners()) signing.apkContentsSigners else signing.signingCertificateHistory
    return signatures.map { it.toCharsString() }.toSet()
  }
}
