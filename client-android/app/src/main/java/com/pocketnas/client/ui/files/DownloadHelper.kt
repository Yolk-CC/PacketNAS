package com.pocketnas.client.ui.files

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import com.pocketnas.client.data.api.ApiClient
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

/** 把服务器文件/目录（zip）保存到系统 Download 目录。 */
object DownloadHelper {

    /**
     * 下载 [path]（目录自动打包 zip）到 Download，返回展示用文件名。
     * 调用方需在协程中执行并自行处理异常。
     */
    suspend fun download(context: Context, api: ApiClient, path: String, isDir: Boolean): String =
        withContext(Dispatchers.IO) {
            val base = PathStack.baseName(path).ifEmpty { "share" }
            val fileName = if (isDir) "$base.zip" else base
            if (Build.VERSION.SDK_INT >= 29) {
                downloadViaMediaStore(context, api, path, isDir, fileName)
            } else {
                @Suppress("DEPRECATION")
                val dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
                val target = uniqueFile(dir, fileName)
                target.outputStream().use { out -> api.downloadTo(path, zip = isDir, out = out) }
                target.name
            }
        }

    private fun downloadViaMediaStore(
        context: Context,
        api: ApiClient,
        path: String,
        isDir: Boolean,
        fileName: String,
    ): String {
        val mime = if (isDir) "application/zip" else guessMime(fileName)
        val values = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, fileName)
            put(MediaStore.Downloads.MIME_TYPE, mime)
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        val uri: Uri = context.contentResolver
            .insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
            ?: error("MediaStore insert failed")
        try {
            context.contentResolver.openOutputStream(uri)!!.use { out ->
                api.downloadTo(path, zip = isDir, out = out)
            }
            val done = ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }
            context.contentResolver.update(uri, done, null, null)
            return fileName
        } catch (e: Exception) {
            context.contentResolver.delete(uri, null, null)
            throw e
        }
    }

    /** 避免覆盖同名文件：a.zip → a (1).zip。 */
    private fun uniqueFile(dir: File, name: String): File {
        var f = File(dir, name)
        var i = 1
        val dot = name.lastIndexOf('.')
        while (f.exists()) {
            val n = if (dot > 0) "${name.substring(0, dot)} ($i)${name.substring(dot)}" else "$name ($i)"
            f = File(dir, n)
            i++
        }
        return f
    }

    private fun guessMime(name: String): String =
        android.webkit.MimeTypeMap.getSingleton()
            .getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase())
            ?: "application/octet-stream"
}
