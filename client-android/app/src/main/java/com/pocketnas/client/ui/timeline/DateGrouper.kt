package com.pocketnas.client.ui.timeline

import com.pocketnas.client.data.model.MediaItem
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** Rows rendered by the timeline adapter: sticky date headers + media cells. */
sealed interface TimelineRow {
    data class Header(val label: String, val epochDay: Long) : TimelineRow
    data class Media(val item: MediaItem) : TimelineRow
}

/**
 * Pure date-grouping logic (SPEC-M9 §3), unit-tested on the JVM.
 * Groups a taken_time-DESC media list into headers + media rows.
 */
object DateGrouper {
    private val FULL: DateTimeFormatter = DateTimeFormatter.ofPattern("yyyy-MM-dd")

    fun dateLabel(epochDay: Long, today: LocalDate): String = when (val date = LocalDate.ofEpochDay(epochDay)) {
        today -> "今天"
        today.minusDays(1) -> "昨天"
        else -> FULL.format(date)
    }

    fun group(
        items: List<MediaItem>,
        today: LocalDate = LocalDate.now(),
        zone: ZoneId = ZoneId.systemDefault(),
    ): List<TimelineRow> {
        val rows = ArrayList<TimelineRow>(items.size + 16)
        var lastDay: Long? = null
        for (item in items) {
            val day = Instant.ofEpochSecond(item.takenTime).atZone(zone).toLocalDate().toEpochDay()
            if (day != lastDay) {
                rows += TimelineRow.Header(dateLabel(day, today), day)
                lastDay = day
            }
            rows += TimelineRow.Media(item)
        }
        return rows
    }
}
