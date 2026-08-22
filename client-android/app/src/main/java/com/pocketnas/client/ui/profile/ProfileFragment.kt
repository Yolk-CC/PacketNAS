package com.pocketnas.client.ui.profile

import android.content.Intent
import android.os.Bundle
import android.view.View
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AlertDialog
import androidx.fragment.app.Fragment
import androidx.lifecycle.lifecycleScope
import com.pocketnas.client.App
import com.pocketnas.client.BuildConfig
import com.pocketnas.client.R
import com.pocketnas.client.data.api.ApiClient
import com.pocketnas.client.data.api.ApiException
import com.pocketnas.client.data.model.ServerEntry
import com.pocketnas.client.ui.server.ServerConnectActivity
import kotlinx.coroutines.launch

/**
 * 「我的」tab（SPEC-M14 §1）：当前服务器卡片、已保存服务器列表（切换/删除）、
 * 添加服务器入口（复用 ServerConnectActivity 的 LAN 发现与连接逻辑）、关于区。
 */
class ProfileFragment : Fragment(R.layout.fragment_profile) {

    private val app: App get() = App.of(requireContext())

    override fun onViewCreated(view: View, savedInstanceState: Bundle?) {
        view.findViewById<TextView>(R.id.text_version).text =
            getString(R.string.about_version, BuildConfig.VERSION_NAME)
        view.findViewById<Button>(R.id.btn_add_server).setOnClickListener {
            startActivity(Intent(requireContext(), ServerConnectActivity::class.java))
        }
        view.findViewById<Button>(R.id.btn_disconnect).setOnClickListener { confirmDisconnect() }
        renderCurrentServer(view)
        renderSavedServers(view)
    }

    private fun renderCurrentServer(root: View) {
        val server = app.server ?: return
        root.findViewById<TextView>(R.id.text_server_name).text = server.name
        root.findViewById<TextView>(R.id.text_server_address).text =
            getString(R.string.profile_server_address, server.host, server.port)
        val status = root.findViewById<TextView>(R.id.text_server_status)
        status.text = getString(R.string.profile_status_checking)
        viewLifecycleOwner.lifecycleScope.launch {
            val ok = try {
                app.apiClient?.systemInfo() != null
            } catch (e: Exception) {
                false
            }
            if (view != null) {
                status.text = getString(
                    if (ok) R.string.profile_status_connected else R.string.profile_status_failed,
                )
            }
        }
    }

    private fun renderSavedServers(root: View) {
        val container = root.findViewById<LinearLayout>(R.id.container_saved)
        container.removeAllViews()
        for (server in app.serverStore.servers()) {
            val row = layoutInflater.inflate(R.layout.item_server, container, false)
            row.findViewById<TextView>(R.id.text_server).text =
                getString(R.string.discovered_entry, server.name, server.host, server.port)
            row.findViewById<Button>(R.id.btn_use).apply {
                setText(R.string.btn_switch_server)
                setOnClickListener { switchTo(server) }
            }
            row.findViewById<Button>(R.id.btn_delete).setOnClickListener {
                confirmRemove(server)
            }
            container.addView(row)
        }
    }

    /** 复用连接逻辑：用已保存 token 校验 /api/system/info，成功后整体刷新。 */
    private fun switchTo(entry: ServerEntry) {
        viewLifecycleOwner.lifecycleScope.launch {
            try {
                ApiClient(entry.baseUrl, tokenProvider = { entry.token }).systemInfo()
                app.switchServer(entry)
                // 简单可靠地刷新所有 tab：重建 MainActivity（fragment 状态各自重新加载）。
                activity?.recreate()
            } catch (e: ApiException) {
                toast(if (e.isUnauthorized || e.httpCode == 403) R.string.error_auth else R.string.error_switch_server)
            } catch (e: Exception) {
                toast(R.string.error_switch_server)
            }
        }
    }

    private fun confirmRemove(entry: ServerEntry) {
        AlertDialog.Builder(requireContext())
            .setMessage(getString(R.string.confirm_remove_server, entry.name))
            .setPositiveButton(android.R.string.ok) { _, _ ->
                val wasCurrent = app.server?.id == entry.id
                app.serverStore.remove(entry.id)
                if (wasCurrent) {
                    // 删除了当前服务器：apiClient 仍指向它，回到连接页重新选择。
                    disconnect()
                } else {
                    view?.let { renderCurrentServer(it); renderSavedServers(it) }
                }
            }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    private fun confirmDisconnect() {
        AlertDialog.Builder(requireContext())
            .setMessage(R.string.confirm_disconnect)
            .setPositiveButton(android.R.string.ok) { _, _ -> disconnect() }
            .setNegativeButton(android.R.string.cancel, null)
            .show()
    }

    /** 断开当前服务器：清除当前 ApiClient 并回到 ServerConnectActivity。 */
    private fun disconnect() {
        app.disconnect()
        startActivity(Intent(requireContext(), ServerConnectActivity::class.java))
        activity?.finish()
    }

    private fun toast(res: Int) =
        Toast.makeText(requireContext(), res, Toast.LENGTH_LONG).show()
}
