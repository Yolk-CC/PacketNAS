package com.pocketnas.client

import com.pocketnas.client.data.model.MediaItem
import com.pocketnas.client.ui.viewer.MediaDetails
import org.junit.Assert.assertEquals
import org.junit.Test

class MediaDetailsTest {

    private fun item(
        name: String = "IMG_0001.jpg",
        mime: String = "image/jpeg",
        width: Int = 4000,
        height: Int = 3000,
        path: String = "/dcim/IMG_0001.jpg",
    ) = MediaItem(
        path = path,
        name = name,
        mimeType = mime,
        takenTime = 1_700_000_000,
        width = width,
        height = height,
    )

    @Test
    fun `resolution formats width and height`() {
        assertEquals("4000 × 3000", MediaDetails.resolution(item()))
    }

    @Test
    fun `resolution falls back to placeholder when dimensions missing`() {
        assertEquals(MediaDetails.PLACEHOLDER, MediaDetails.resolution(item(width = 0, height = 0)))
        assertEquals(MediaDetails.PLACEHOLDER, MediaDetails.resolution(item(width = 0)))
    }

    @Test
    fun `file size is a placeholder (metadata has no size field)`() {
        assertEquals(MediaDetails.PLACEHOLDER, MediaDetails.fileSize(item()))
    }

    @Test
    fun `empty fields fall back to placeholder`() {
        val it = item(name = "", mime = "", path = "")
        assertEquals(MediaDetails.PLACEHOLDER, MediaDetails.name(it))
        assertEquals(MediaDetails.PLACEHOLDER, MediaDetails.mimeType(it))
        assertEquals(MediaDetails.PLACEHOLDER, MediaDetails.path(it))
    }
}
