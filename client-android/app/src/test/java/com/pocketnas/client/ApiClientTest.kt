package com.pocketnas.client.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test

class ApiClientTest {

    private lateinit var server: MockWebServer
    private lateinit var api: ApiClient

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = ApiClient(server.url("/").toString().trimEnd('/'), tokenProvider = { "tok123" })
    }

    @After
    fun tearDown() = server.shutdown()

    @Test
    fun `gallery parses items and sends token`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """{"total":1,"items":[{"path":"/DCIM/a.jpg","name":"a.jpg",
                "mimeType":"image/jpeg","takenTime":1723692622,"width":4000,"height":3000,
                "duration":0,"thumbUrl":"/api/thumb/DCIM/a.jpg?w=300&h=300",
                "isLivePhoto":true,"liveType":"ios"}]}"""
            )
        )
        val resp = api.gallery(0, 200)
        assertEquals(1, resp.total)
        assertEquals("/DCIM/a.jpg", resp.items[0].path)
        assertTrue(resp.items[0].isLivePhoto)
        val req = server.takeRequest()
        assertEquals("tok123", req.getHeader("X-Auth-Token"))
        assertEquals("/api/gallery?offset=0&limit=200&type=all", req.path)
    }

    @Test
    fun `login returns token`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"token":"abc"}"""))
        assertEquals("abc", api.login("pw"))
        val req = server.takeRequest()
        assertEquals("/api/auth/login", req.path)
        assertTrue(req.body.readUtf8().contains("pw"))
    }

    @Test
    fun `http error raises ApiException`() = runBlocking {
        server.enqueue(MockResponse().setResponseCode(401).setBody("{}"))
        try {
            api.systemInfo()
            error("expected ApiException")
        } catch (e: ApiException) {
            assertTrue(e.isUnauthorized)
        }
    }

    @Test
    fun `mediaUrl escapes each path segment`() {
        val url = api.mediaUrl("/api/thumb", "/DCIM/相 册/a b.jpg", "w=300&h=300")
        assertTrue(url.startsWith(server.url("/").toString()))
        assertTrue(url.contains("/api/thumb/DCIM/"))
        assertTrue(!url.contains("相")) // non-ASCII encoded
        assertTrue(url.contains("a%20b.jpg"))
        assertTrue(url.endsWith("?w=300&h=300"))
    }

    @Test
    fun `delete sends paths`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        api.deleteFiles(listOf("/a.jpg", "/b.jpg"))
        val req = server.takeRequest()
        assertEquals("DELETE", req.method)
        assertEquals("/api/files", req.path)
        assertTrue(req.body.readUtf8().contains("/b.jpg"))
    }
}
