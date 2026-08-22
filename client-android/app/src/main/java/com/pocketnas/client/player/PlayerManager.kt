package com.pocketnas.client.player

import android.content.Context
import androidx.media3.common.MediaItem as ExoMediaItem
import androidx.media3.common.Player
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import okhttp3.OkHttpClient

/**
 * ExoPlayer (Media3) factory for the viewer (SPEC-M9 §4). Media URLs are
 * authenticated via the same X-Auth-Token header as the API client, injected
 * by the shared [OkHttpClient]'s auth interceptor (M15b: one client per app,
 * not one per player).
 */
object PlayerManager {

    fun create(context: Context, httpClient: OkHttpClient): ExoPlayer {
        val factory = OkHttpDataSource.Factory(httpClient)
        return ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(context).setDataSourceFactory(factory))
            .build()
    }

    fun play(player: ExoPlayer, url: String) {
        player.setMediaItem(ExoMediaItem.fromUri(url))
        player.prepare()
        player.playWhenReady = true
        player.repeatMode = Player.REPEAT_MODE_ONE
    }
}
