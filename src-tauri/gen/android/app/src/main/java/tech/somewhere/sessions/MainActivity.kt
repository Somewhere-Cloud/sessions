package tech.somewhere.sessions

import android.graphics.Color
import android.os.Bundle
import android.os.SystemClock
import android.view.View
import android.webkit.JavascriptInterface
import android.webkit.WebView
import android.widget.Toast
import androidx.activity.OnBackPressedCallback
import androidx.activity.SystemBarStyle
import androidx.activity.enableEdgeToEdge
import androidx.core.view.ViewCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat

class MainActivity : TauriActivity() {
  private lateinit var webView: WebView
  private var backDecisionPending = false
  private var firstRootBackAt = 0L
  private var exitToast: Toast? = null

  override fun onCreate(savedInstanceState: Bundle?) {
    // Sessions starts dark (the connect screen is always dark); the web UI
    // reports its own theme through SessionsAndroid.setSystemBarsDark.
    enableEdgeToEdge(SystemBarStyle.dark(Color.TRANSPARENT), SystemBarStyle.dark(Color.TRANSPARENT))
    super.onCreate(savedInstanceState)
  }

  // Called from the shared UI (lib/tauriBridge.ts) when its theme changes, so
  // the status and navigation bar icons stay readable on the app's background.
  inner class SystemBarsBridge {
    @JavascriptInterface
    fun setSystemBarsDark(dark: Boolean) {
      runOnUiThread {
        if (!::webView.isInitialized) return@runOnUiThread
        WindowInsetsControllerCompat(window, webView).apply {
          isAppearanceLightStatusBars = !dark
          isAppearanceLightNavigationBars = !dark
        }
        // The status-bar and gesture-handle strips match the app surface.
        findViewById<View>(android.R.id.content).setBackgroundColor(if (dark) DARK_NAV else LIGHT_NAV)
      }
    }
  }

  override fun onWebViewCreate(webView: WebView) {
    this.webView = webView
    if (BuildConfig.SESSIONS_WEB_DEBUG) WebView.setWebContentsDebuggingEnabled(true)
    webView.addJavascriptInterface(SystemBarsBridge(), "SessionsAndroid")
    // Wry calls this before setContentView, so the WebView has no parent yet;
    // the activity's content frame is the container it is about to join.
    keepAboveNavigationAndKeyboard(findViewById(android.R.id.content))
    onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
      override fun handleOnBackPressed() {
        if (dismissKeyboard()) {
          resetExitConfirmation()
          return
        }
        if (backDecisionPending) return

        backDecisionPending = true
        webView.evaluateJavascript(
          """
          (() => {
            try {
              return window.__SESSIONS_ANDROID_BACK__?.() === true;
            } catch (_) {
              return false;
            }
          })()
          """.trimIndent()
        ) { handled ->
          backDecisionPending = false
          if (handled == "true") {
            resetExitConfirmation()
          } else {
            handleRootBack()
          }
        }
      }
    })
  }

  // Edge-to-edge, with the insets applied natively: Android WebView versions
  // disagree about which system insets reach env(safe-area-inset-*), and
  // Chromium 113 reports none once insets are rewritten. Pad the WebView's
  // container by the system bars, display cutout and keyboard, and hand the
  // WebView consumed insets, so env() is zero on every WebView and nothing is
  // counted twice. The keyboard shrinks the page, keeping focused fields and
  // the terminal keys visible.
  private fun keepAboveNavigationAndKeyboard(host: View) {
    ViewCompat.setOnApplyWindowInsetsListener(host) { view, insets ->
      val bars = insets.getInsets(WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout())
      val ime = insets.getInsets(WindowInsetsCompat.Type.ime())
      view.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, ime.bottom))
      WindowInsetsCompat.CONSUMED
    }
    ViewCompat.requestApplyInsets(host)
  }

  private fun dismissKeyboard(): Boolean {
    if (!::webView.isInitialized) return false
    val insets = ViewCompat.getRootWindowInsets(webView) ?: return false
    if (!insets.isVisible(WindowInsetsCompat.Type.ime())) return false
    WindowInsetsControllerCompat(window, webView).hide(WindowInsetsCompat.Type.ime())
    return true
  }

  private fun handleRootBack() {
    val now = SystemClock.elapsedRealtime()
    if (firstRootBackAt != 0L && now - firstRootBackAt <= EXIT_CONFIRMATION_MS) {
      exitToast?.cancel()
      // Leave the app the way Android 12+ leaves a root activity: move the
      // task to the background. Finishing tears down Tauri's event loop while
      // the WebView still holds it, which aborted natively in September.
      moveTaskToBack(true)
      firstRootBackAt = 0L
      return
    }

    firstRootBackAt = now
    exitToast?.cancel()
    exitToast = Toast.makeText(this, "Press back again to exit", Toast.LENGTH_SHORT).also {
      it.show()
    }
  }

  private fun resetExitConfirmation() {
    firstRootBackAt = 0L
    exitToast?.cancel()
    exitToast = null
  }

  companion object {
    private const val EXIT_CONFIRMATION_MS = 2_000L
    private const val DARK_NAV = 0xFF131516.toInt() // --bg-base, dark theme
    private const val LIGHT_NAV = 0xFFF7F8FA.toInt() // --bg-base, light theme
  }
}
