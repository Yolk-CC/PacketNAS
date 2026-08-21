package com.pocketnas.client.data

import android.content.Context
import com.pocketnas.client.data.model.ServerEntry
import org.json.JSONArray
import org.json.JSONObject

/** Lightweight multi-server persistence via SharedPreferences (SPEC-M9 §2). */
class ServerStore(context: Context) {

    private val prefs = context.getSharedPreferences("servers", Context.MODE_PRIVATE)

    fun servers(): List<ServerEntry> {
        val raw = prefs.getString(KEY_LIST, "[]") ?: "[]"
        val arr = JSONArray(raw)
        return (0 until arr.length()).map { i ->
            val o = arr.getJSONObject(i)
            ServerEntry(
                id = o.getString("id"),
                host = o.getString("host"),
                port = o.getInt("port"),
                name = o.optString("name", o.getString("id")),
                token = o.optString("token", ""),
            )
        }
    }

    fun upsert(server: ServerEntry) {
        val list = servers().filterNot { it.id == server.id }.toMutableList()
        list.add(0, server)
        save(list)
    }

    fun remove(id: String) {
        save(servers().filterNot { it.id == id })
        if (prefs.getString(KEY_CURRENT, null) == id) {
            prefs.edit().remove(KEY_CURRENT).apply()
        }
    }

    fun updateToken(id: String, token: String) {
        save(servers().map { if (it.id == id) it.copy(token = token) else it })
    }

    fun current(): ServerEntry? {
        val id = prefs.getString(KEY_CURRENT, null) ?: return servers().firstOrNull()
        return servers().firstOrNull { it.id == id } ?: servers().firstOrNull()
    }

    fun setCurrent(id: String) {
        prefs.edit().putString(KEY_CURRENT, id).apply()
    }

    private fun save(list: List<ServerEntry>) {
        val arr = JSONArray()
        for (s in list) {
            arr.put(JSONObject().apply {
                put("id", s.id)
                put("host", s.host)
                put("port", s.port)
                put("name", s.name)
                put("token", s.token)
            })
        }
        prefs.edit().putString(KEY_LIST, arr.toString()).apply()
    }

    companion object {
        private const val KEY_LIST = "server_list"
        private const val KEY_CURRENT = "current_server"
    }
}
