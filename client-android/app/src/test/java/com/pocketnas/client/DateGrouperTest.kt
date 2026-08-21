package com.pocketnas.client.ui.timeline

import com.pocketnas.client.data.model.MediaItem
import org.junit.Assert.assertEquals
import org.junit.Test
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneOffset

class DateGrouperTest {

    private val zone = ZoneOffset.UTC
    private val today = LocalDate.of(2024, 8, 15)

    private fun item(path: String, taken: Long) = MediaItem(
        path = path, name = path, mimeType = "image/jpeg", takenTime = taken,
    )

    private fun epochOf(date: String): Long =
        Instant.parse("${date}T12:00:00Z").epochSecond

    @Test
    fun `groups by date desc with headers`() {
        val items = listOf(
            item("/a.jpg", epochOf("2024-08-15")),
            item("/b.jpg", epochOf("2024-08-15")),
            item("/c.jpg", epochOf("2024-08-13")),
        )
        val rows = DateGrouper.group(items, today = today, zone = zone)
        assertEquals(5, rows.size)
        assertEquals(TimelineRow.Header("今天", today.toEpochDay()), rows[0])
        assertEquals(TimelineRow.Media(items[0]), rows[1])
        assertEquals(TimelineRow.Media(items[1]), rows[2])
        assertEquals(TimelineRow.Header("2024-08-13", LocalDate.of(2024, 8, 13).toEpochDay()), rows[3])
        assertEquals(TimelineRow.Media(items[2]), rows[4])
    }

    @Test
    fun `yesterday gets local label`() {
        assertEquals("昨天", DateGrouper.dateLabel(today.minusDays(1).toEpochDay(), today))
        assertEquals("今天", DateGrouper.dateLabel(today.toEpochDay(), today))
        assertEquals("2024-01-01", DateGrouper.dateLabel(LocalDate.of(2024, 1, 1).toEpochDay(), today))
    }

    @Test
    fun `empty list produces no rows`() {
        assertEquals(emptyList<TimelineRow>(), DateGrouper.group(emptyList(), today, zone))
    }

    @Test
    fun `no duplicate headers for same day`() {
        val items = (0 until 10).map { item("/$it.jpg", epochOf("2024-08-15")) }
        val headers = DateGrouper.group(items, today, zone).filterIsInstance<TimelineRow.Header>()
        assertEquals(1, headers.size)
    }
}
