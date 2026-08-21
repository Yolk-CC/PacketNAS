package com.pocketnas.client.data.discovery

import android.util.Log
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.InetAddress

/**
 * LAN discovery (SPEC-M8 §1): broadcast "POCKETNAS_DISCOVER" to UDP 45777,
 * collect "POCKETNAS_HERE|<serverName>|<port>|<apiLevel>" replies.
 */
object Discovery {
    const val PORT = 45777
    const val REQUEST = "POCKETNAS_DISCOVER"
    const val REPLY_PREFIX = "POCKETNAS_HERE"
    const val TIMEOUT_MS = 2000

    data class DiscoveredServer(
        val name: String,
        val host: String,
        val port: Int,
        val apiLevel: Int,
    )

    /** Pure parser, unit-tested. Returns null for malformed replies. */
    fun parseReply(text: String, host: String): DiscoveredServer? {
        val parts = text.trim().split("|")
        if (parts.size != 4 || parts[0] != REPLY_PREFIX) return null
        val port = parts[2].toIntOrNull() ?: return null
        val apiLevel = parts[3].toIntOrNull() ?: return null
        if (port !in 1..65535) return null
        return DiscoveredServer(parts[1], host, port, apiLevel)
    }

    /** Sends one broadcast and collects replies for [TIMEOUT_MS]. Blocking. */
    suspend fun discover(): List<DiscoveredServer> = withContext(Dispatchers.IO) {
        val found = LinkedHashMap<String, DiscoveredServer>()
        var socket: DatagramSocket? = null
        try {
            socket = DatagramSocket().apply {
                broadcast = true
                soTimeout = 300
            }
            val payload = REQUEST.toByteArray(Charsets.UTF_8)
            val targets = listOf(
                InetAddress.getByName("255.255.255.255"),
            )
            for (target in targets) {
                socket.send(DatagramPacket(payload, payload.size, target, PORT))
            }
            val deadline = System.currentTimeMillis() + TIMEOUT_MS
            val buf = ByteArray(512)
            while (System.currentTimeMillis() < deadline) {
                val packet = DatagramPacket(buf, buf.size)
                try {
                    socket.receive(packet)
                } catch (e: java.net.SocketTimeoutException) {
                    continue
                }
                val text = String(packet.data, packet.offset, packet.length, Charsets.UTF_8)
                parseReply(text, packet.address.hostAddress ?: continue)?.let {
                    found["${it.host}:${it.port}"] = it
                }
            }
        } catch (e: Exception) {
            Log.w("Discovery", "LAN discovery failed", e)
        } finally {
            socket?.close()
        }
        found.values.toList()
    }
}
