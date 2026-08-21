package com.pocketnas.client.data.model

import kotlinx.serialization.Serializable

/** One entry of GET /api/gallery (internal/media/handlers.go galleryItem). */
@Serializable
data class MediaItem(
    val path: String,
    val name: String,
    val mimeType: String,
    val takenTime: Long, // epoch seconds
    val width: Int = 0,
    val height: Int = 0,
    val duration: Int = 0, // ms for videos, 0 for images
    val thumbUrl: String = "",
    val isLivePhoto: Boolean = false,
    val liveType: String = "",
    val resolutions: List<String> = emptyList(),
) {
    val isVideo: Boolean get() = mimeType.startsWith("video/")
    val isImage: Boolean get() = mimeType.startsWith("image/")
}

@Serializable
data class GalleryResponse(
    val total: Int,
    val items: List<MediaItem>,
)

/** GET /api/system/info (internal/files/handlers.go SystemInfo). */
@Serializable
data class SystemInfo(
    val version: String = "",
    val serverName: String = "",
    val apiLevel: Int = 0,
    val diskFree: Long = 0,
    val diskTotal: Long = 0,
)

@Serializable
data class LoginRequest(val password: String)

@Serializable
data class LoginResponse(val token: String)

/** GET /api/gallery/scan */
@Serializable
data class ScanStatus(
    val scanning: Boolean = false,
    val indexed: Int = 0,
)

@Serializable
data class DeleteRequest(val paths: List<String>)

/** One entry of GET /api/files (internal/files/service.go FileInfo). */
@Serializable
data class FileInfo(
    val name: String,
    val path: String, // root-relative slash path, leading "/"
    val size: Long = 0,
    val modified: Long = 0, // epoch seconds
    val isDir: Boolean = false,
    val mimeType: String = "",
) {
    val isImage: Boolean get() = mimeType.startsWith("image/")
    val isVideo: Boolean get() = mimeType.startsWith("video/")
    val isMedia: Boolean get() = isImage || isVideo
}

@Serializable
data class MkdirRequest(val dir: String, val name: String)

@Serializable
data class RenameRequest(val path: String, val newName: String)

@Serializable
data class UploadResponse(val uploaded: List<String> = emptyList())

/** A saved server connection (SPEC-M9 §2). */
data class ServerEntry(
    val id: String, // stable id, e.g. "host:port"
    val host: String,
    val port: Int,
    val name: String,
    val token: String = "",
) {
    val baseUrl: String get() = "http://$host:$port"
}
