package com.pocketnas.client.ui.server

import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.lifecycle.lifecycleScope
import com.pocketnas.client.App
import com.pocketnas.client.R
import com.pocketnas.client.data.api.ApiClient
import com.pocketnas.client.data.api.ApiException
import com.pocketnas.client.data.discovery.Discovery
import com.pocketnas.client.data.model.ServerEntry
import com.pocketnas.client.ui.timeline.TimelineActivity
import kotlinx.coroutines.launch

/**
 * Connection page (SPEC-M9 §2): manual host:port + optional password,
 * LAN scan, saved server list with switch/delete.
 */
class ServerConnectActivity : AppCompatActivity() {

    private lateinit var app: App

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        app = App.of(this)
        setContentView(R.layout.activity_server_connect)
        renderSavedServers()

        findViewById<Button>(R.id.btn_connect).setOnClickListener { connectManual() }
        findViewById<Button>(R.id.btn_scan).setOnClickListener { scanLan() }
    }

    private fun connectManual() {
        val input = findViewById<EditText>(R.id.edit_host).text.toString().trim()
        val password = findViewById<EditText>(R.id.edit_password).text.toString()
        if (input.isEmpty()) {
            toast(R.string.error_host_required)
            return
        }
        val parts = input.removePrefix("http://").removePrefix("https://").split(":")
        val host = parts[0]
        val port = parts.getOrNull(1)?.toIntOrNull() ?: 8080
        verifyAndSave(ServerEntry(id = "$host:$port", host = host, port = port, name = input), password)
    }

    private fun scanLan() {
        val container = findViewById<LinearLayout>(R.id.container_discovered)
        val progress = findViewById<ProgressBar>(R.id.progress_scan)
        container.removeAllViews()
        progress.visibility = View.VISIBLE
        lifecycleScope.launch {
            val results = Discovery.discover()
            progress.visibility = View.GONE
            if (results.isEmpty()) {
                toast(R.string.discover_none)
                return@launch
            }
            for (server in results) {
                val btn = Button(this@ServerConnectActivity).apply {
                    text = getString(R.string.discovered_entry, server.name, server.host, server.port)
                    setOnClickListener {
                        verifyAndSave(
                            ServerEntry(
                                id = "${server.host}:${server.port}",
                                host = server.host,
                                port = server.port,
                                name = server.name,
                            ),
                            findViewById<EditText>(R.id.edit_password).text.toString(),
                        )
                    }
                }
                container.addView(btn)
            }
        }
    }

    /** Verify via /api/system/info (apiLevel>=2), login if needed, then save. */
    private fun verifyAndSave(entry: ServerEntry, password: String) {
        val progress = findViewById<ProgressBar>(R.id.progress_scan)
        progress.visibility = View.VISIBLE
        lifecycleScope.launch {
            try {
                val api = ApiClient(entry.baseUrl, tokenProvider = { "" })
                val info = api.systemInfo()
                if (info.apiLevel < MIN_API_LEVEL) {
                    toast(R.string.error_api_level)
                    return@launch
                }
                var token = ""
                if (password.isNotEmpty()) {
                    token = api.login(password)
                }
                val saved = entry.copy(
                    name = info.serverName.ifEmpty { entry.name },
                    token = token,
                )
                app.serverStore.upsert(saved)
                app.switchServer(saved)
                openTimeline()
            } catch (e: ApiException) {
                toast(if (e.isUnauthorized || e.httpCode == 403) R.string.error_auth else R.string.error_connect)
            } catch (e: Exception) {
                toast(R.string.error_connect)
            } finally {
                progress.visibility = View.GONE
            }
        }
    }

    private fun renderSavedServers() {
        val container = findViewById<LinearLayout>(R.id.container_saved)
        container.removeAllViews()
        for (server in app.serverStore.servers()) {
            val row = layoutInflater.inflate(R.layout.item_server, container, false)
            row.findViewById<TextView>(R.id.text_server).text =
                getString(R.string.discovered_entry, server.name, server.host, server.port)
            row.findViewById<Button>(R.id.btn_use).setOnClickListener {
                app.switchServer(server)
                openTimeline()
            }
            row.findViewById<Button>(R.id.btn_delete).setOnClickListener {
                app.serverStore.remove(server.id)
                renderSavedServers()
            }
            container.addView(row)
        }
    }

    private fun openTimeline() {
        startActivity(Intent(this, TimelineActivity::class.java))
        finish()
    }

    private fun toast(res: Int) = Toast.makeText(this, res, Toast.LENGTH_LONG).show()

    companion object {
        const val MIN_API_LEVEL = 2 // SPEC-M8 §1
    }
}
