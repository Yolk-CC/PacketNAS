package com.pocketnas.client.player

import android.content.Context
import androidx.media3.common.MediaItem as ExoMediaItem
import androidx.media3.common.Player
import androidx.media3.datasource.okhttp.OkHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import com.pocketnas.client.data.api.ApiClient
import okhttp3.OkHttpClient

/**
 * ExoPlayer (Media3) factory for the viewer (SPEC-M9 §4). Media URLs are
 * authenticated via the same X-Auth-Token header as the API client.
 */
object PlayerManager {

    fun create(context: Context): ExoPlayer {
        val factory = OkHttpDataSource.Factory(
            OkHttpClient.Builder()
                .addInterceptor { chain ->
                    chain.proceed(
                        chain.request().newBuilder()
                            .header("X-Auth-Token", tokenProvider?.invoke().orEmpty())
                            .build()
                    )
                }
                .build()
        )
        return ExoPlayer.Builder(context)
            .setMediaSourceFactory(DefaultMediaSourceFactory(context).setDataSourceFactory(factory))
            .build()
    }

    /** Set by the viewer so the player can authenticate against the server. */
    var tokenProvider: (() -> String)? = null

    fun play(player: ExoPlayer, url: String) {
        player.setMediaItem(ExoMediaItem.fromUri(url))
        player.prepare()
        player.playWhenReady = true
        player.repeatMode = Player.REPEAT_MODE_ONE
    }
}
