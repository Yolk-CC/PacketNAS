package com.pocketnas.app

import android.annotation.SuppressLint
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.Color
import android.net.Uri
import android.os.Bundle
import android.view.Gravity
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import java.net.NetworkInterface

/**
 * Single-activity shell: starts NasService (Go core) and loads the Web UI
 * in a full-screen WebView. Also bootstraps storage/battery permissions.
 */
class MainActivity : ComponentActivity() {

    companion object {
        private const val REQUEST_STORAGE = 42
    }

    private lateinit var webView: WebView
    private lateinit var statusBar: TextView
    private var filePathCallback: ValueCallback<Array<Uri>>? = null
    private lateinit var fileChooserLauncher: ActivityResultLauncher<Intent>

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // File-picker bridge for WebView uploads (<input type=file>).
        fileChooserLauncher = registerForActivityResult(
            ActivityResultContracts.StartActivityForResult()
        ) { result ->
            val callback = filePathCallback ?: return@registerForActivityResult
            val data = result.data
            var uris: Array<Uri>? = null
            if (result.resultCode == RESULT_OK && data != null) {
                uris = WebChromeClient.FileChooserParams.parseResult(result.resultCode, data)
            }
            callback.onReceiveValue(uris)
            filePathCallback = null
        }

        setupWebView()
        // Top status bar: running state + LAN address, tap to copy (SPEC-M8 §2).
        statusBar = TextView(this).apply {
            setTextColor(Color.WHITE)
            setBackgroundColor(Color.rgb(33, 33, 33))
            gravity = Gravity.CENTER_VERTICAL
            setPadding(32, 0, 32, 0)
            text = getString(R.string.status_starting)
            setOnClickListener { copyLanAddress() }
        }
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            addView(statusBar, LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT,
                (48 * resources.displayMetrics.density).toInt()
            ))
            addView(webView, LinearLayout.LayoutParams(
                LinearLayout.LayoutParams.MATCH_PARENT, 0, 1f
            ))
        }
        setContentView(root)

        // Permissions first (storage is required for the Go core to index),
        // then start the service; the WebView loads once the server is up.
        if (!PermissionHelper.hasStorageAccess(this)) {
            PermissionHelper.requestStorageAccess(this, REQUEST_STORAGE)
        }
        PermissionHelper.requestIgnoreBatteryOptimizations(this)
        startNasService()
        loadWhenReady()
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun setupWebView() {
        webView = WebView(this)
        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true // the UI keeps its token in localStorage
            allowFileAccess = true
            allowContentAccess = true
            cacheMode = WebSettings.LOAD_DEFAULT
        }
        webView.webViewClient = WebViewClient() // keep navigation in the WebView
        webView.webChromeClient = object : WebChromeClient() {
            override fun onShowFileChooser(
                view: WebView?,
                callback: ValueCallback<Array<Uri>>?,
                params: FileChooserParams?
            ): Boolean {
                filePathCallback?.onReceiveValue(null)
                filePathCallback = callback
                val intent = params?.createIntent() ?: Intent(Intent.ACTION_GET_CONTENT).apply {
                    addCategory(Intent.CATEGORY_OPENABLE)
                    type = "*/*"
                }
                return try {
                    fileChooserLauncher.launch(intent)
                    true
                } catch (e: Exception) {
                    filePathCallback = null
                    false
                }
            }
        }
    }

    private fun startNasService() {
        val intent = Intent(this, NasService::class.java).apply {
            putExtra(NasService.EXTRA_ROOT, NasService.DEFAULT_ROOT)
            putExtra(NasService.EXTRA_PORT, NasService.DEFAULT_PORT)
        }
        startForegroundService(intent)
    }

    /** Poll until the Go core reports its address, then load the UI. */
    private fun loadWhenReady() {
        val existing = NasService.serverAddress
        if (existing.isNotEmpty()) {
            loadUi(existing)
            return
        }
        webView.postDelayed({
            if (isFinishing || isDestroyed) return@postDelayed
            val addr = NasService.serverAddress
            if (addr.isNotEmpty()) {
                loadUi(addr)
            } else {
                loadWhenReady()
            }
        }, 200)
    }

    private fun loadUi(addr: String) {
        // Always reach the server via loopback, regardless of bind address.
        val local = addr
            .replace("0.0.0.0", "127.0.0.1")
            .replace("[::]", "127.0.0.1")
        updateStatusBar(addr)
        webView.loadUrl(local)
    }

    /** Status bar shows the LAN-reachable URL; tap it to copy. */
    private fun updateStatusBar(addr: String) {
        val port = addr.substringAfterLast(':', "8080")
        val lanIp = lanAddress()
        lanUrl = if (lanIp != null) "http://$lanIp:$port" else localLanUrl(addr)
        statusBar.text = getString(R.string.status_running, lanUrl)
    }

    private var lanUrl: String = ""

    private fun localLanUrl(addr: String) = addr
        .replace("0.0.0.0", "127.0.0.1")
        .replace("[::]", "127.0.0.1")

    /** First non-loopback IPv4 address of any up interface, or null. */
    private fun lanAddress(): String? = try {
        NetworkInterface.getNetworkInterfaces().toList()
            .filter { it.isUp && !it.isLoopback }
            .flatMap { it.inetAddresses.toList() }
            .firstOrNull { !it.isLoopbackAddress && it.hostAddress?.contains(':') == false }
            ?.hostAddress
    } catch (e: Exception) {
        null
    }

    private fun copyLanAddress() {
        if (lanUrl.isEmpty()) return
        val cm = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        cm.setPrimaryClip(ClipData.newPlainText("PocketNAS address", lanUrl))
        Toast.makeText(this, R.string.status_copied, Toast.LENGTH_SHORT).show()
    }

    override fun onBackPressed() {
        if (::webView.isInitialized && webView.canGoBack()) {
            webView.goBack()
        } else {
            super.onBackPressed()
        }
    }

    override fun onDestroy() {
        if (::webView.isInitialized) {
            webView.destroy()
        }
        super.onDestroy()
    }
}
