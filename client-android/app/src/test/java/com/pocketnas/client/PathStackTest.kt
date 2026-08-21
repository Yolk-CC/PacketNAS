package com.pocketnas.client.ui.files

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PathStackTest {

    @Test
    fun `root by default`() {
        val s = PathStack()
        assertEquals("/", s.path)
        assertTrue(s.isRoot)
        assertEquals(0, s.depth)
    }

    @Test
    fun `push and pop`() {
        val s = PathStack()
        s.push("照片")
        s.push("2024 旅行")
        assertEquals("/照片/2024 旅行", s.path)
        assertEquals(2, s.depth)
        assertTrue(s.pop())
        assertEquals("/照片", s.path)
        assertTrue(s.pop())
        assertFalse(s.pop()) // root 不能再退
        assertTrue(s.isRoot)
    }

    @Test
    fun `push rejects traversal and empty`() {
        val s = PathStack()
        listOf("", "..", ".", " / ").forEach {
            try {
                s.push(it)
                throw AssertionError("should reject: $it")
            } catch (e: IllegalArgumentException) {
                // expected
            }
        }
        assertTrue(s.isRoot)
    }

    @Test
    fun `push trims slashes`() {
        val s = PathStack()
        s.push("/a/")
        assertEquals("/a", s.path)
    }

    @Test
    fun `initial path normalized`() {
        assertEquals("/a/b", PathStack("/a//b/").path)
        assertTrue(PathStack("//").isRoot)
    }

    @Test
    fun `breadcrumbs carry labels and full paths`() {
        val s = PathStack("/共享 A/子 目 录/c")
        val crumbs = s.breadcrumbs()
        assertEquals(3, crumbs.size)
        assertEquals("共享 A" to "/共享 A", crumbs[0])
        assertEquals("子 目 录" to "/共享 A/子 目 录", crumbs[1])
        assertEquals("c" to "/共享 A/子 目 录/c", crumbs[2])
    }

    @Test
    fun `popTo clamps`() {
        val s = PathStack("/a/b/c")
        s.popTo(1)
        assertEquals("/a", s.path)
        s.popTo(99)
        assertEquals("/a", s.path)
        s.popTo(-1)
        assertTrue(s.isRoot)
    }

    @Test
    fun `child builds full path`() {
        assertEquals("/a/b.txt", PathStack("/a").child("b.txt"))
        assertEquals("/b.txt", PathStack().child("b.txt"))
    }

    @Test
    fun `parent and baseName`() {
        assertEquals("/a", PathStack.parent("/a/b"))
        assertEquals("/", PathStack.parent("/a"))
        assertEquals("/", PathStack.parent("/"))
        assertEquals("b", PathStack.baseName("/a/b"))
        assertEquals("", PathStack.baseName("/"))
    }
}
