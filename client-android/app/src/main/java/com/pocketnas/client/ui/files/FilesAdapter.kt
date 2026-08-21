package com.pocketnas.client.ui.files

import android.text.format.Formatter
import android.view.LayoutInflater
import android.view.View
import android.view.ViewGroup
import android.widget.ImageView
import android.widget.TextView
import androidx.recyclerview.widget.DiffUtil
import androidx.recyclerview.widget.ListAdapter
import androidx.recyclerview.widget.RecyclerView
import com.pocketnas.client.R
import com.pocketnas.client.data.model.FileInfo
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** 文件列表（目录📁/图片🖼️/视频🎬/文件📄 图标用 emoji，选中打勾高亮）。 */
class FilesAdapter(
    private val selection: SelectionSet,
    private val onClick: (FileInfo) -> Unit,
    private val onLongClick: (FileInfo) -> Unit,
) : ListAdapter<FileInfo, FilesAdapter.VH>(DIFF) {

    /** 多选模式开关：影响勾选图标显示。 */
    var selectionMode = false
        set(value) {
            if (field != value) {
                field = value
                notifyDataSetChanged()
            }
        }

    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int): VH {
        val v = LayoutInflater.from(parent.context).inflate(R.layout.item_file, parent, false)
        return VH(v)
    }

    override fun onBindViewHolder(holder: VH, position: Int) {
        val item = getItem(position)
        val ctx = holder.itemView.context
        holder.icon.text = when {
            item.isDir -> "📁"
            item.isImage -> "🖼️"
            item.isVideo -> "🎬"
            else -> "📄"
        }
        holder.name.text = item.name
        holder.meta.text = buildString {
            if (!item.isDir) append(Formatter.formatShortFileSize(ctx, item.size))
            if (item.modified > 0) {
                if (isNotEmpty()) append(" · ")
                append(TIME_FMT.format(Instant.ofEpochSecond(item.modified).atZone(ZoneId.systemDefault())))
            }
        }
        val selected = selection.contains(item.path)
        holder.check.visibility = if (selectionMode) View.VISIBLE else View.GONE
        holder.check.setImageResource(
            if (selected) android.R.drawable.checkbox_on_background
            else android.R.drawable.checkbox_off_background
        )
        holder.itemView.isActivated = selected
        holder.itemView.setOnClickListener { onClick(item) }
        holder.itemView.setOnLongClickListener {
            onLongClick(item)
            true
        }
    }

    class VH(view: View) : RecyclerView.ViewHolder(view) {
        val icon: TextView = view.findViewById(R.id.text_icon)
        val name: TextView = view.findViewById(R.id.text_name)
        val meta: TextView = view.findViewById(R.id.text_meta)
        val check: ImageView = view.findViewById(R.id.icon_check)
    }

    companion object {
        private val TIME_FMT = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm")

        private val DIFF = object : DiffUtil.ItemCallback<FileInfo>() {
            override fun areItemsTheSame(a: FileInfo, b: FileInfo) = a.path == b.path
            override fun areContentsTheSame(a: FileInfo, b: FileInfo) = a == b
        }
    }
}
