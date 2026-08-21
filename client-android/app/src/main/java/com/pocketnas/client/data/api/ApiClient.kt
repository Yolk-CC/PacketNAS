package com.pocketnas.client.data.api

import com.pocketnas.client.data.model.DeleteRequest
import com.pocketnas.client.data.model.FileInfo
import com.pocketnas.client.data.model.GalleryResponse
import com.pocketnas.client.data.model.LoginRequest
import com.pocketnas.client.data.model.LoginResponse
import com.pocketnas.client.data.model.MkdirRequest
import com.pocketnas.client.data.model.RenameRequest
import com.pocketnas.client.data.model.ScanStatus
import com.pocketnas.client.data.model.UploadResponse
import com.pocketnas.client.data.model.SystemInfo
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.serializer
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.MultipartBody
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.io.OutputStream
import java.util.concurrent.TimeUnit

/**
 * API surface used by the client (SPEC-M9 §2-§4). All endpoints exist on the
 * M8 server; every request carries the X-Auth-Token header (SPEC.md / M1).
 */
interface PocketNasApi {
    suspend fun systemInfo(): SystemInfo
    suspend fun login(password: String): String
    suspend fun gallery(offset: Int, limit: Int, type: String = "all"): GalleryResponse
    suspend fun galleryScan(): ScanStatus
    suspend fun deleteFiles(paths: List<String>)

    // SPEC-M10: folder browsing (routes per internal/server/server.go).
    suspend fun listFiles(path: String, type: String = "all"): List<FileInfo>
    suspend fun mkdir(dir: String, name: String)
    suspend fun rename(path: String, newName: String)
    suspend fun upload(dir: String, fileName: String, body: RequestBody): UploadResponse

    /** Streams GET /api/download/<path>[?archive=zip] into [out]. */
    suspend fun downloadTo(path: String, zip: Boolean, out: OutputStream)
}

class ApiException(val httpCode: Int, message: String) : IOException(message) {
    val isUnauthorized: Boolean get() = httpCode == 401
}

/** Builds authenticated clients and absolute media URLs for one server. */
class ApiClient(
    baseUrl: String,
    private val tokenProvider: () -> String,
    private val client: OkHttpClient = defaultHttpClient(),
) : PocketNasApi {

    private val base = baseUrl.trimEnd('/')
    private val json = Json { ignoreUnknownKeys = true }

    /** Builds an absolute URL for a path-style endpoint, escaping each
     *  segment of [relPath] (server paths start with "/"). */
    fun mediaUrl(prefix: String, relPath: String, query: String? = null): String {
        val builder = (base.toHttpUrl()).newBuilder()
        prefix.split('/').filter { it.isNotEmpty() }.forEach { builder.addPathSegment(it) }
        relPath.split('/').filter { it.isNotEmpty() }.forEach { builder.addPathSegment(it) }
        query?.let { builder.encodedQuery(it) }
        return builder.build().toString()
    }

    fun absolute(urlOrPath: String): String =
        if (urlOrPath.startsWith("http")) urlOrPath else base + urlOrPath

    override suspend fun systemInfo(): SystemInfo =
        get("/api/system/info")

    override suspend fun login(password: String): String {
        val body = json.encodeToString(LoginRequest.serializer(), LoginRequest(password))
            .toRequestBody(JSON)
        val resp = execute(Request.Builder().url("$base/api/auth/login").post(body).build())
        return json.decodeFromString(LoginResponse.serializer(), resp).token
    }

    override suspend fun gallery(offset: Int, limit: Int, type: String): GalleryResponse =
        get("/api/gallery?offset=$offset&limit=$limit&type=$type")

    override suspend fun galleryScan(): ScanStatus = get("/api/gallery/scan")

    override suspend fun deleteFiles(paths: List<String>) {
        val body = json.encodeToString(DeleteRequest.serializer(), DeleteRequest(paths))
            .toRequestBody(JSON)
        execute(Request.Builder().url("$base/api/files").delete(body).build())
    }

    override suspend fun listFiles(path: String, type: String): List<FileInfo> {
        val url = base.toHttpUrl().newBuilder()
            .addPathSegment("api").addPathSegment("files")
            .addQueryParameter("path", path)
            .addQueryParameter("type", type)
            .build()
        val resp = execute(Request.Builder().url(url).get().build())
        return json.decodeFromString(serializer<List<FileInfo>>(), resp)
    }

    override suspend fun mkdir(dir: String, name: String) {
        val body = json.encodeToString(MkdirRequest.serializer(), MkdirRequest(dir, name))
            .toRequestBody(JSON)
        execute(Request.Builder().url("$base/api/files/mkdir").post(body).build())
    }

    override suspend fun rename(path: String, newName: String) {
        val body = json.encodeToString(RenameRequest.serializer(), RenameRequest(path, newName))
            .toRequestBody(JSON)
        execute(Request.Builder().url("$base/api/files/rename").post(body).build())
    }

    override suspend fun upload(dir: String, fileName: String, body: RequestBody): UploadResponse {
        val url = base.toHttpUrl().newBuilder()
            .addPathSegment("api").addPathSegment("upload")
            .addQueryParameter("path", dir)
            .build()
        val multipart = MultipartBody.Builder()
            .setType(MultipartBody.FORM)
            .addFormDataPart("file", fileName, body)
            .build()
        val resp = execute(Request.Builder().url(url).post(multipart).build())
        return json.decodeFromString(UploadResponse.serializer(), resp)
    }

    override suspend fun downloadTo(path: String, zip: Boolean, out: OutputStream) {
        withContext(Dispatchers.IO) {
            val builder = base.toHttpUrl().newBuilder().addPathSegment("api").addPathSegment("download")
            path.split('/').filter { it.isNotEmpty() }.forEach { builder.addPathSegment(it) }
            if (zip) builder.addQueryParameter("archive", "zip")
            val req = Request.Builder().url(builder.build()).get()
                .header("X-Auth-Token", tokenProvider())
                .build()
            client.newCall(req).execute().use { resp ->
                if (!resp.isSuccessful) {
                    throw ApiException(resp.code, "HTTP ${resp.code}")
                }
                resp.body?.byteStream()?.use { it.copyTo(out) }
                    ?: throw ApiException(resp.code, "empty body")
            }
        }
    }

    private suspend inline fun <reified T> get(path: String): T {
        val resp = execute(Request.Builder().url(base + path).get().build())
        return json.decodeFromString(serializer<T>(), resp)
    }

    private suspend fun execute(request: Request): String = withContext(Dispatchers.IO) {
        val req = request.newBuilder()
            .header("X-Auth-Token", tokenProvider())
            .build()
        client.newCall(req).execute().use { resp ->
            val text = resp.body?.string().orEmpty()
            if (!resp.isSuccessful) {
                throw ApiException(resp.code, "HTTP ${resp.code}: $text")
            }
            text
        }
    }

    companion object {
        private val JSON = "application/json; charset=utf-8".toMediaType()

        fun defaultHttpClient(): OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(8, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .build()
    }
}
