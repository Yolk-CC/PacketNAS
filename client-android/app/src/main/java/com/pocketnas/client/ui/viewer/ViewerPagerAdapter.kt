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
import com.pocketnas.client.R
import com.pocketnas.client.data.api.ApiClient
import com.pocketnas.client.data.model.MediaItem
import com.pocketnas.client.player.PlayerManager

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

    @SuppressLint("NotifyDataSetChanged")
    fun submit(list: List<MediaItem>) {
        items.clear()
        items.addAll(list)
        notifyDataSetChanged()
    }

    override fun getItemCount(): Int = items.size

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): PageVH {
        val view = LayoutInflater.from(parent.context).inflate(R.layout.item_viewer_page, parent, false)
        return PageVH(view)
    }

    override fun onBindViewHolder(holder: PageVH, position: Int) {
        holder.bind(items[position])
    }

    override fun onViewRecycled(holder: PageVH) {
        holder.releasePlayer()
        super.onViewRecycled(holder)
    }

    inner class PageVH(view: View) : RecyclerView.ViewHolder(view) {
        private val photo: PhotoView = view.findViewById(R.id.photo_view)
        private val playerView: PlayerView = view.findViewById(R.id.player_view)
        private var player: ExoPlayer? = null
        private var livePlaying = false

        @SuppressLint("ClickableViewAccessibility")
        fun bind(item: MediaItem) {
            val client = api() ?: return
            releasePlayer()
            playerView.visibility = View.GONE
            photo.visibility = View.VISIBLE
            photo.setZoomable(true)

            if (item.isVideo) {
                // Videos play directly via /api/media/file/<path> (Range OK).
                playerView.visibility = View.VISIBLE
                photo.visibility = View.GONE
                val p = PlayerManager.create(itemView.context).also { player = it }
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
                    MotionEvent.ACTION_DOWN -> itemView.postDelayed({
                        livePlaying = true
                        photo.visibility = View.VISIBLE
                        playerView.visibility = View.VISIBLE
                        val p = PlayerManager.create(itemView.context).also { player = it }
                        playerView.player = p
                        api()?.let { PlayerManager.play(p, it.mediaUrl("/api/livephoto", item.path)) }
                    }, 400)
                    MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                        itemView.handler?.removeCallbacksAndMessages(null)
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

        fun releasePlayer() {
            playerView.player = null
            player?.release()
            player = null
        }
    }
}
