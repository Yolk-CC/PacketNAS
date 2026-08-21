package com.pocketnas.client.ui.files

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class SelectionSetTest {

    @Test
    fun `toggle adds then removes`() {
        val s = SelectionSet()
        assertTrue(s.toggle("/a"))
        assertTrue(s.contains("/a"))
        assertFalse(s.toggle("/a"))
        assertTrue(s.isEmpty)
    }

    @Test
    fun `snapshot preserves insertion order and is a copy`() {
        val s = SelectionSet()
        s.select("/b")
        s.select("/a")
        val snap = s.snapshot()
        assertEquals(listOf("/b", "/a"), snap)
        s.clear()
        assertEquals(2, snap.size)
        assertTrue(s.isEmpty)
    }

    @Test
    fun `duplicates counted once`() {
        val s = SelectionSet()
        s.select("/a")
        s.select("/a")
        assertEquals(1, s.size)
    }

    @Test
    fun `retainAll drops vanished entries`() {
        val s = SelectionSet()
        s.select("/a")
        s.select("/b")
        s.select("/c")
        s.retainAll(listOf("/b", "/c"))
        assertFalse(s.contains("/a"))
        assertEquals(listOf("/b", "/c"), s.snapshot())
        s.retainAll(emptyList())
        assertTrue(s.isEmpty)
    }
}
