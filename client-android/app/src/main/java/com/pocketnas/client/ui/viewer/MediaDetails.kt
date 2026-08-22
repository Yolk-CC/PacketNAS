package com.pocketnas.client.ui.viewer

import com.pocketnas.client.data.model.MediaItem

/**
 * Pure mapping of [MediaItem] metadata to detail-sheet values (SPEC-M14 §2).
 * Fields missing from the gallery metadata (e.g. file size) fall back to
 * [PLACEHOLDER]; no new server API is introduced.
 */
object MediaDetails {

    const val PLACEHOLDER = "-"

    /** "宽 × 高"，元数据缺失时为占位符。 */
    fun resolution(item: MediaItem): String =
        if (item.width > 0 && item.height > 0) "${item.width} × ${item.height}" else PLACEHOLDER

    /** gallery 元数据暂无文件大小字段，显示占位符。 */
    fun fileSize(item: MediaItem): String = PLACEHOLDER

    fun mimeType(item: MediaItem): String = item.mimeType.ifEmpty { PLACEHOLDER }

    fun name(item: MediaItem): String = item.name.ifEmpty { PLACEHOLDER }

    fun path(item: MediaItem): String = item.path.ifEmpty { PLACEHOLDER }
}
