package dev.ferry.app.transfer

import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Environment
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import dev.ferry.app.data.TFile
import java.io.OutputStream
import java.util.UUID

/** Scoped-storage helpers: received files go to Download/Ferry via MediaStore (no storage permission needed). */
object Storage {
    fun mimeFor(name: String): String =
        MimeTypeMap.getSingleton().getMimeTypeFromExtension(name.substringAfterLast('.', "").lowercase()) ?: "application/octet-stream"

    /** Creates a hidden (IS_PENDING) entry; it becomes visible to other apps after [publish]. */
    fun createPending(ctx: Context, name: String, mime: String, subdirs: List<String> = emptyList()): Uri {
        val rel = (listOf(Environment.DIRECTORY_DOWNLOADS, "Ferry") + subdirs).joinToString("/") + "/"
        val v = ContentValues().apply {
            put(MediaStore.Downloads.DISPLAY_NAME, name)
            put(MediaStore.Downloads.MIME_TYPE, mime.ifEmpty { mimeFor(name) })
            put(MediaStore.Downloads.RELATIVE_PATH, rel)
            put(MediaStore.Downloads.IS_PENDING, 1)
        }
        return ctx.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, v)
            ?: throw IllegalStateException("Couldn't create a file in Downloads. Is storage full?")
    }

    fun openAppend(ctx: Context, uri: Uri): OutputStream =
        ctx.contentResolver.openOutputStream(uri, "wa") ?: throw IllegalStateException("Couldn't open file for writing")

    fun openTruncate(ctx: Context, uri: Uri): OutputStream =
        ctx.contentResolver.openOutputStream(uri, "wt") ?: throw IllegalStateException("Couldn't open file for writing")

    fun publish(ctx: Context, uri: Uri) {
        ctx.contentResolver.update(uri, ContentValues().apply { put(MediaStore.Downloads.IS_PENDING, 0) }, null, null)
    }

    fun delete(ctx: Context, uri: Uri) {
        runCatching { ctx.contentResolver.delete(uri, null, null) }
    }

    fun length(ctx: Context, uri: Uri): Long =
        runCatching { ctx.contentResolver.openFileDescriptor(uri, "r")?.use { it.statSize } ?: 0L }.getOrDefault(0L)

    /**
     * Everything a share-sheet intent carries: streams (EXTRA_STREAM, else ClipData) or, for plain text/links, a small .txt file
     * written to the cache so it can be sent like any other file.
     */
    fun sharedUris(ctx: Context, intent: android.content.Intent): List<Uri> {
        val streams = when (intent.action) {
            android.content.Intent.ACTION_SEND -> listOfNotNull(androidx.core.content.IntentCompat.getParcelableExtra(intent, android.content.Intent.EXTRA_STREAM, Uri::class.java))
            android.content.Intent.ACTION_SEND_MULTIPLE -> androidx.core.content.IntentCompat.getParcelableArrayListExtra(intent, android.content.Intent.EXTRA_STREAM, Uri::class.java).orEmpty()
            else -> return emptyList()
        }.ifEmpty { intent.clipData?.let { c -> (0 until c.itemCount).mapNotNull { c.getItemAt(it).uri } }.orEmpty() }
        if (streams.isNotEmpty()) return streams
        val text = intent.getStringExtra(android.content.Intent.EXTRA_TEXT)?.takeIf { it.isNotBlank() } ?: return emptyList()
        return runCatching {
            val isLink = text.trim().let { (it.startsWith("http://") || it.startsWith("https://")) && !it.contains(' ') }
            val base = (intent.getStringExtra(android.content.Intent.EXTRA_SUBJECT) ?: if (isLink) "Shared link" else "Shared text")
                .replace(Regex("[\\/:*?\"<>|\n\r]"), " ").trim().take(60).ifEmpty { "Shared text" }
            val dir = java.io.File(ctx.cacheDir, "shared").apply { mkdirs() }
            val f = java.io.File(dir, "$base-${System.currentTimeMillis() % 100000}.txt").apply { writeText(text) }
            listOf(Uri.fromFile(f))
        }.getOrDefault(emptyList())
    }

    /** Reads name/size/type of a document the user picked or shared into the app. */
    fun describe(ctx: Context, uri: Uri): TFile? = runCatching {
        // Keep read access across app restarts so interrupted transfers can resume.
        runCatching { ctx.contentResolver.takePersistableUriPermission(uri, android.content.Intent.FLAG_GRANT_READ_URI_PERMISSION) }
        // Must be readable now; otherwise the transfer would fail later with a confusing error.
        ctx.contentResolver.openFileDescriptor(uri, "r")?.close() ?: return null
        var name = uri.lastPathSegment ?: "file"
        var size = -1L
        ctx.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
            if (c.moveToFirst()) {
                c.getString(0)?.let { name = it }
                if (!c.isNull(1)) size = c.getLong(1)
            }
        }
        if (size < 0) size = length(ctx, uri)
        val mime = ctx.contentResolver.getType(uri) ?: mimeFor(name)
        TFile(UUID.randomUUID().toString(), name, size, mime, uri)
    }.getOrNull()
}
