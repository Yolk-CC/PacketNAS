package com.pocketnas.app

import android.annotation.SuppressLint
import android.content.Intent
import android.net.Uri
import android.os.Bundle
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.activity.ComponentActivity
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts

/**
 * Single-activity shell: starts NasService (Go core) and loads the Web UI
 * in a full-screen WebView. Also bootstraps storage/battery permissions.
 */
class MainActivity : ComponentActivity() {

    companion object {
        private const val REQUEST_STORAGE = 42
    }

    private lateinit var webView: WebView
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
        setContentView(webView)

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
        webView.loadUrl(local)
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
