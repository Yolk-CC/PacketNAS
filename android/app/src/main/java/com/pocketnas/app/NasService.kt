package com.pocketnas.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Intent
import android.os.Build
import android.os.IBinder
import android.util.Log
import mobile.Mobile
import java.io.File

/**
 * Foreground service hosting the Go core (via the gomobile binding).
 * Starts the server in onCreate, stops it in onDestroy, and keeps a
 * persistent notification while running.
 */
class NasService : Service() {

    companion object {
        private const val TAG = "NasService"
        private const val CHANNEL_ID = "pocketnas_service"
        private const val NOTIFICATION_ID = 1

        const val EXTRA_ROOT = "root"
        const val EXTRA_PASSWORD = "password"
        const val EXTRA_PORT = "port"

        const val DEFAULT_ROOT = "/storage/emulated/0"
        const val DEFAULT_PORT = 8080

        /** Actual base URL once the Go core is up, e.g. "http://0.0.0.0:8080". */
        @Volatile
        var serverAddress: String = ""
            private set
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        startForeground(NOTIFICATION_ID, buildNotification())
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (serverAddress.isNotEmpty()) {
            return START_STICKY // already running
        }
        val root = intent?.getStringExtra(EXTRA_ROOT) ?: DEFAULT_ROOT
        val password = intent?.getStringExtra(EXTRA_PASSWORD) ?: ""
        val port = intent?.getIntExtra(EXTRA_PORT, DEFAULT_PORT) ?: DEFAULT_PORT

        // The binding call blocks until the listener is bound; keep it off
        // the main thread.
        Thread {
            // gomobile maps Go int → Java long.
            // M11: pass the onnxruntime-mobile native lib if packaged; "" →
            // face recognition degrades to faces_unavailable.
            val onnxLib = File(applicationInfo.nativeLibraryDir, "libonnxruntime.so")
                .takeIf { it.exists() }?.absolutePath ?: ""
            val addr = Mobile.start(root, password, port.toLong(), onnxLib)
            if (addr.isNullOrEmpty()) {
                Log.e(TAG, "Go core failed to start")
                stopSelf()
            } else {
                serverAddress = addr
                Log.i(TAG, "PocketNAS core listening on $addr (root: $root)")
            }
        }.start()
        return START_STICKY
    }

    override fun onDestroy() {
        try {
            Mobile.stop()
        } catch (t: Throwable) {
            Log.w(TAG, "error stopping Go core", t)
        }
        serverAddress = ""
        super.onDestroy()
    }

    private fun buildNotification(): Notification {
        val mgr = getSystemService(NOTIFICATION_SERVICE) as NotificationManager
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                getString(R.string.notification_channel),
                NotificationManager.IMPORTANCE_LOW
            )
            mgr.createNotificationChannel(channel)
        }
        val tap = PendingIntent.getActivity(
            this, 0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
        )
        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            Notification.Builder(this, CHANNEL_ID)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(this)
        }
        return builder
            .setContentTitle(getString(R.string.app_name))
            .setContentText(getString(R.string.notification_text))
            .setSmallIcon(android.R.drawable.ic_menu_save)
            .setContentIntent(tap)
            .setOngoing(true)
            .build()
    }
}
