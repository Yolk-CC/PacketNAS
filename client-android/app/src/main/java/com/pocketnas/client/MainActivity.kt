package com.pocketnas.client

import android.content.Intent
import android.os.Bundle
import androidx.appcompat.app.AppCompatActivity
import com.pocketnas.client.ui.server.ServerConnectActivity
import com.pocketnas.client.ui.timeline.TimelineActivity

/** Entry router: saved server → timeline, otherwise the connect page. */
class MainActivity : AppCompatActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val next = if (App.of(this).apiClient != null) {
            TimelineActivity::class.java
        } else {
            ServerConnectActivity::class.java
        }
        startActivity(Intent(this, next))
        finish()
    }
}
