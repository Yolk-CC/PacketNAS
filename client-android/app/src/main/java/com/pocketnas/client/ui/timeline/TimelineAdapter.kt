package com.pocketnas.client.ui.timeline

import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import coil.load
import com.pocketnas.client.R
import com.pocketnas.client.data.api.ApiClient
import java.util.Locale

class TimelineAdapter(
    private val api: () -> ApiClient?,
    private val onClick: (position: Int, item: com.pocketnas.client.data.model.MediaItem) -> Unit,
) : ListAdapter<TimelineRow, RecyclerView.ViewHolder>(DIFF) {

    var spanCount = 3

    override fun getItemViewType(position: Int): Int = when (getItem(position)) {
        is TimelineRow.Header -> TYPE_HEADER
        is TimelineRow.Media -> TYPE_MEDIA
    }

    fun mediaIndexOf(position: Int): Int {
        // Index among Media rows only (used for ViewPager initial position).
        var idx = 0
        for (i in 0 until position) if (getItem(i) is TimelineRow.Media) idx++
        return idx
    }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): RecyclerView.ViewHolder {
        val inf = LayoutInflater.from(parent.context)
        return if (viewType == TYPE_HEADER) {
            HeaderVH(inflate(inf, parent, R.layout.item_date_header))
        } else {
            MediaVH(inflate(inf, parent, R.layout.item_media))
        }
    }

    private fun inflate(inf: LayoutInflater, parent: ViewGroup, layout: Int) =
        inf.inflate(layout, parent, false)

    override fun onBindViewHolder(holder: RecyclerView.ViewHolder, position: Int) {
        when (val row = getItem(position)) {
            is TimelineRow.Header -> (holder as HeaderVH).bind(row)
            is TimelineRow.Media -> (holder as MediaVH).bind(row.item, position)
        }
    }

    class HeaderVH(view: View) : RecyclerView.ViewHolder(view) {
        private val label: TextView = view.findViewById(R.id.text_date)
        fun bind(row: TimelineRow.Header) {
            label.text = row.label
        }
    }

    inner class MediaVH(view: View) : RecyclerView.ViewHolder(view) {
        private val thumb: ImageView = view.findViewById(R.id.image_thumb)
        private val duration: TextView = view.findViewById(R.id.badge_duration)
        private val live: TextView = view.findViewById(R.id.badge_live)

        fun bind(item: com.pocketnas.client.data.model.MediaItem, position: Int) {
            val client = api()
            val url = if (item.thumbUrl.isNotEmpty()) {
                client?.absolute(item.thumbUrl)
            } else {
                client?.mediaUrl("/api/thumb", item.path, "w=300&h=300")
            }
            thumb.load(url) {
                placeholder(R.drawable.bg_thumb_placeholder)
                error(R.drawable.bg_thumb_placeholder)
                memoryCacheKey(item.path)
            }
            if (item.isVideo && item.duration > 0) {
                duration.visibility = View.VISIBLE
                duration.text = formatDuration(item.duration)
            } else {
                duration.visibility = View.GONE
            }
            live.visibility = if (item.isLivePhoto) View.VISIBLE else View.GONE
            itemView.setOnClickListener { onClick(mediaIndexOf(position), item) }
        }

        private fun formatDuration(ms: Int): String {
            val totalSec = ms / 1000
            return String.format(Locale.US, "%d:%02d", totalSec / 60, totalSec % 60)
        }
    }

    companion object {
        const val TYPE_HEADER = 0
        const val TYPE_MEDIA = 1

        private val DIFF = object : DiffUtil.ItemCallback<TimelineRow>() {
            override fun areItemsTheSame(a: TimelineRow, b: TimelineRow): Boolean = when {
                a is TimelineRow.Header && b is TimelineRow.Header -> a.epochDay == b.epochDay
                a is TimelineRow.Media && b is TimelineRow.Media -> a.item.path == b.item.path
                else -> false
            }

            override fun areContentsTheSame(a: TimelineRow, b: TimelineRow): Boolean = a == b
        }
    }
}
