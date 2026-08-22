package com.pocketnas.client.ui.viewer

import android.annotation.SuppressLint
import android.view.LayoutInflater
import android.view.MotionEvent
import android.view.View
import android.view.ViewGroup
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.ui.PlayerView
import androidx.recyclerview.widget.RecyclerView
import coil.load
import com.github.chrisbanes.photoview.PhotoView
import com.pocketnas.client.App
import com.pocketnas.client.R
import com.pocketnas.client.data.api.ApiClient
import com.pocketnas.client.data.model.MediaItem
import com.pocketnas.client.player.PlayerManager
import com.pocketnas.client.util.PendingDelayedAction
import okhttp3.OkHttpClient

/**
 * ViewPager2 pages: PhotoView for images (double-tap / pinch zoom), an
 * ExoPlayer PlayerView for videos, and a long-press Live Photo overlay
 * playing /api/livephoto/<path> (SPEC-M9 §4).
 */
class ViewerPagerAdapter(
    private val api: () -> ApiClient?,
    private val onToggleUi: () -> Unit,
) : RecyclerView.Adapter<ViewerPagerAdapter.PageVH>() {

    private val items = mutableListOf<MediaItem>()

    /** Holders still attached to pages; onViewRecycled is not guaranteed on
     *  Activity teardown, so ViewerActivity.onDestroy releases them via
     *  [releaseAll]. */
    private val activeHolders = mutableSetOf<PageVH>()

    @SuppressLint("NotifyDataSetChanged")
    fun submit(list: List<MediaItem>) {
        items.clear()
        items.addAll(list)
        notifyDataSetChanged()
    }

    override fun getItemCount(): Int = items.size

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): PageVH {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_viewer_page, parent, false)
        return PageVH(view).also { activeHolders += it }
    }

    override fun onBindViewHolder(holder: PageVH, position: Int) {
        holder.bind(items[position])
    }

    override fun onViewRecycled(holder: PageVH) {
        holder.cancelPendingLivePhoto()
        holder.releasePlayer()
        activeHolders -= holder
        super.onViewRecycled(holder)
    }

    /** Releases every still-attached page's player (Activity onDestroy). */
    fun releaseAll() {
        activeHolders.forEach {
            it.cancelPendingLivePhoto()
            it.releasePlayer()
        }
    }

    inner class PageVH(view: View) : RecyclerView.ViewHolder(view) {
        private val photo: PhotoView = view.findViewById(R.id.photo_view)
        private val playerView: PlayerView = view.findViewById(R.id.player_view)
        private var player: ExoPlayer? = null
        private var livePlaying = false

        /** 400ms long-press trigger; cancelled precisely, never via
         *  removeCallbacksAndMessages(null) which wipes unrelated callbacks. */
        private val livePhotoTrigger = PendingDelayedAction(
            postDelayed = { r, delay -> itemView.postDelayed(r, delay) },
            removeCallbacks = { r -> itemView.removeCallbacks(r) },
        )

        @SuppressLint("ClickableViewAccessibility")
        fun bind(item: MediaItem) {
            val client = api() ?: return
            cancelPendingLivePhoto()
            releasePlayer()
            playerView.visibility = View.GONE
            photo.visibility = View.VISIBLE
            photo.setZoomable(true)

            if (item.isVideo) {
                // Videos play directly via /api/media/file/<path> (Range OK).
                playerView.visibility = View.VISIBLE
                photo.visibility = View.GONE
                val p = PlayerManager.create(itemView.context, httpClient()).also { player = it }
                playerView.player = p
                PlayerManager.play(p, client.mediaUrl("/api/media/file", item.path))
                playerView.setOnClickListener { onToggleUi() }
            } else {
                // Thumbnail first, then the original downscaled to ~2x screen.
                val fullUrl = client.mediaUrl("/api/media/file", item.path)
                photo.load(fullUrl) {
                    // Show the grid thumbnail (cached under the path key) while
                    // the original loads; Coil downscales to the view size.
                    placeholderMemoryCacheKey(item.path)
                }
                photo.setOnPhotoTapListener { _, _, _ -> onToggleUi() }

                if (item.isLivePhoto) {
                    attachLivePhotoGesture(item)
                } else {
                    photo.setOnLongClickListener(null)
                }
            }
        }

        /** Long-press plays the motion part over the still photo. */
        @SuppressLint("ClickableViewAccessibility")
        private fun attachLivePhotoGesture(item: MediaItem) {
            photo.setOnTouchListener { _, event ->
                when (event.actionMasked) {
                    MotionEvent.ACTION_DOWN -> livePhotoTrigger.schedule(LIVE_PRESS_DELAY_MS) {
                        livePlaying = true
                        photo.visibility = View.VISIBLE
                        playerView.visibility = View.VISIBLE
                        val p = PlayerManager.create(itemView.context, httpClient()).also { player = it }
                        playerView.player = p
                        api()?.let { PlayerManager.play(p, it.mediaUrl("/api/livephoto", item.path)) }
                    }
                    MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                        livePhotoTrigger.cancel()
                        if (livePlaying) {
                            livePlaying = false
                            playerView.visibility = View.GONE
                            releasePlayer()
                        }
                    }
                }
                false // keep PhotoView gestures working
            }
        }

        /** Cancels a still-pending long-press trigger (bind/recycle/destroy). */
        fun cancelPendingLivePhoto() = livePhotoTrigger.cancel()

        fun releasePlayer() {
            playerView.player = null
            player?.release()
            player = null
        }

        private fun httpClient(): OkHttpClient = App.of(itemView.context).httpClient
    }

    private companion object {
        const val LIVE_PRESS_DELAY_MS = 400L
    }
}
