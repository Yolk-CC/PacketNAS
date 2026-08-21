package com.pocketnas.client

import android.app.Application
import coil.ImageLoader
import coil.ImageLoaderFactory
import coil.disk.DiskCache
import coil.memory.MemoryCache
import com.pocketnas.client.data.ServerStore
import com.pocketnas.client.data.api.ApiClient
import com.pocketnas.client.data.model.ServerEntry
import okhttp3.OkHttpClient

/**
 * Application singletons. Provides the current [ServerEntry], an [ApiClient]
 * bound to it, and a Coil [ImageLoader] that injects X-Auth-Token
 * (SPEC-M9 §2: all API requests carry the token).
 */
class App : Application(), ImageLoaderFactory {

    lateinit var serverStore: ServerStore
        private set

    var server: ServerEntry?
        get() = serverStore.current()
        set(value) {
            if (value != null) {
                serverStore.setCurrent(value.id)
                apiClient = newApiClient(value)
            }
        }

    @Volatile
    var apiClient: ApiClient? = null
        private set

    override fun onCreate() {
        super.onCreate()
        serverStore = ServerStore(this)
        serverStore.current()?.let { apiClient = newApiClient(it) }
    }

    fun switchServer(entry: ServerEntry) {
        server = entry
    }

    private fun newApiClient(entry: ServerEntry): ApiClient =
        ApiClient(entry.baseUrl, tokenProvider = {
            serverStore.current()?.token.orEmpty()
        })

    override fun newImageLoader(): ImageLoader = ImageLoader.Builder(this)
        .okHttpClient {
            OkHttpClient.Builder()
                .addInterceptor { chain ->
                    chain.proceed(
                        chain.request().newBuilder()
                            .header("X-Auth-Token", serverStore.current()?.token.orEmpty())
                            .build()
                    )
                }
                .build()
        }
        .memoryCache { MemoryCache.Builder(this).maxSizePercent(0.25).build() }
        .diskCache {
            DiskCache.Builder()
                .directory(cacheDir.resolve("image_cache"))
                .maxSizeBytes(256L * 1024 * 1024)
                .build()
        }
        .crossfade(true)
        .build()

    companion object {
        fun of(context: android.content.Context): App = context.applicationContext as App
    }
}
