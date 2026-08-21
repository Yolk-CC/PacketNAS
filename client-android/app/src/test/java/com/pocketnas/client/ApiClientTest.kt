package com.pocketnas.client.data.api

import kotlinx.coroutines.runBlocking
import okhttp3.mockwebserver.MockResponse
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.mockwebserver.MockWebServer
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
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

    @Test
    fun `listFiles parses and encodes unicode path`() = runBlocking {
        server.enqueue(
            MockResponse().setBody(
                """[{"name":"子 目录","path":"/共享 A/子 目录","size":0,"modified":1723692622,
                "isDir":true,"mimeType":""},
                {"name":"a b.jpg","path":"/共享 A/a b.jpg","size":123,"modified":1723692622,
                "isDir":false,"mimeType":"image/jpeg"}]"""
            )
        )
        val items = api.listFiles("/共享 A")
        assertEquals(2, items.size)
        assertTrue(items[0].isDir)
        assertTrue(items[1].isImage)
        assertFalse(items[1].isVideo)
        val req = server.takeRequest()
        assertEquals("GET", req.method)
        // 中文与空格必须 URL 编码
        assertTrue(req.path!!.startsWith("/api/files?path="))
        assertTrue(!req.path!!.contains("共享"))
        assertTrue(req.path!!.contains("%E5%85%B1%E4%BA%AB"))
        assertTrue(req.path!!.contains("type=all"))
        assertEquals("tok123", req.getHeader("X-Auth-Token"))
    }

    @Test
    fun `mkdir posts dir and name`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        api.mkdir("/照片", "新建 文件夹")
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertEquals("/api/files/mkdir", req.path)
        val body = req.body.readUtf8()
        assertTrue(body.contains("\"dir\":\"/照片\"") || body.contains("照片"))
        assertTrue(body.contains("新建 文件夹"))
    }

    @Test
    fun `rename posts path and newName`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"ok":true}"""))
        api.rename("/a/b.txt", "c.txt")
        val req = server.takeRequest()
        assertEquals("/api/files/rename", req.path)
        val body = req.body.readUtf8()
        assertTrue(body.contains("/a/b.txt"))
        assertTrue(body.contains("c.txt"))
    }

    @Test
    fun `upload sends multipart file field with encoded dir`() = runBlocking {
        server.enqueue(MockResponse().setBody("""{"uploaded":["a b.txt"]}"""))
        val resp = api.upload(
            "/共享 A", "a b.txt",
            "hello".toRequestBody("text/plain".toMediaType())
        )
        assertEquals(listOf("a b.txt"), resp.uploaded)
        val req = server.takeRequest()
        assertEquals("POST", req.method)
        assertTrue(req.path!!.startsWith("/api/upload?path="))
        assertTrue(!req.path!!.contains("共享"))
        assertTrue(req.headers["Content-Type"]!!.startsWith("multipart/form-data"))
        val body = req.body.readUtf8()
        assertTrue(body.contains("name=\"file\"; filename=\"a b.txt\""))
        assertTrue(body.contains("hello"))
    }

    @Test
    fun `downloadTo streams zip for dir`() = runBlocking {
        server.enqueue(MockResponse().setBody("ZIPDATA"))
        val out = java.io.ByteArrayOutputStream()
        api.downloadTo("/共享 A/子 目录", zip = true, out = out)
        assertEquals("ZIPDATA", out.toString())
        val req = server.takeRequest()
        assertEquals("GET", req.method)
        assertTrue(req.path!!.startsWith("/api/download/"))
        assertTrue(req.path!!.contains("archive=zip"))
        assertTrue(!req.path!!.contains("共享")) // encoded
        assertEquals("tok123", req.getHeader("X-Auth-Token"))
    }

    @Test
    fun `downloadTo file without archive param`() = runBlocking {
        server.enqueue(MockResponse().setBody("DATA"))
        val out = java.io.ByteArrayOutputStream()
        api.downloadTo("/a/b.txt", zip = false, out = out)
        assertEquals("DATA", out.toString())
        val req = server.takeRequest()
        assertEquals("/api/download/a/b.txt", req.path)
    }
}