package com.pocketnas.app

import android.app.Activity
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.os.PowerManager
import android.provider.Settings

/**
 * Storage + battery permission bootstrap.
 *
 * Strategy: Android 11+ (API 30) prefers MANAGE_EXTERNAL_STORAGE (all-files
 * access, needed to index the whole shared storage); when not granted we
 * degrade to the granular media permissions (READ_MEDIA_IMAGES/VIDEO on
 * API 33+, READ_EXTERNAL_STORAGE below). Also offers to whitelist the app
 * from battery optimizations so the server is not killed in the background.
 */
object PermissionHelper {

    /** True when we hold at least the degraded set of storage permissions. */
    fun hasStorageAccess(context: Context): Boolean {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            if (Environment.isExternalStorageManager()) return true
        }
        return when {
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU ->
                granted(context, android.Manifest.permission.READ_MEDIA_IMAGES) &&
                    granted(context, android.Manifest.permission.READ_MEDIA_VIDEO)
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.M ->
                granted(context, android.Manifest.permission.READ_EXTERNAL_STORAGE)
            else -> true // runtime permissions did not exist yet
        }
    }

    /** True when all-files access (MANAGE_EXTERNAL_STORAGE) is in effect. */
    fun hasAllFilesAccess(): Boolean =
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.R &&
            Environment.isExternalStorageManager()

    /**
     * Ask the user for storage access. All-files access is requested via its
     * settings screen (no runtime prompt exists for it); the degraded path
     * uses a normal runtime request.
     *
     * @param requestCode used for the runtime-permission fallback request
     */
    fun requestStorageAccess(activity: Activity, requestCode: Int) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R &&
            !Environment.isExternalStorageManager()
        ) {
            try {
                activity.startActivity(
                    Intent(
                        Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
                        Uri.parse("package:${activity.packageName}")
                    )
                )
            } catch (e: Exception) {
                activity.startActivity(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
            }
            return
        }
        val needed = mutableListOf<String>()
        when {
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU -> {
                needed += android.Manifest.permission.READ_MEDIA_IMAGES
                needed += android.Manifest.permission.READ_MEDIA_VIDEO
            }
            Build.VERSION.SDK_INT >= Build.VERSION_CODES.M ->
                needed += android.Manifest.permission.READ_EXTERNAL_STORAGE
        }
        val missing = needed.filter { !granted(activity, it) }
        if (missing.isNotEmpty()) {
            activity.requestPermissions(missing.toTypedArray(), requestCode)
        }
    }

    /** True when the app is already exempt from battery optimizations. */
    fun isIgnoringBatteryOptimizations(context: Context): Boolean {
        val pm = context.getSystemService(Context.POWER_SERVICE) as PowerManager
        return pm.isIgnoringBatteryOptimizations(context.packageName)
    }

    /** Open the system dialog to whitelist the app from battery optimizations. */
    fun requestIgnoreBatteryOptimizations(activity: Activity) {
        if (isIgnoringBatteryOptimizations(activity)) return
        try {
            activity.startActivity(
                Intent(
                    Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
                    Uri.parse("package:${activity.packageName}")
                )
            )
        } catch (e: Exception) {
            activity.startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
        }
    }

    private fun granted(context: Context, permission: String): Boolean =
        context.checkSelfPermission(permission) == PackageManager.PERMISSION_GRANTED
}
