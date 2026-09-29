package dev.ferry.app.tv

import android.content.ActivityNotFoundException
import android.content.ContentUris
import android.content.Context
import android.content.Intent
import android.graphics.ImageDecoder
import android.media.MediaPlayer
import android.net.Uri
import android.os.Environment
import android.provider.MediaStore
import android.widget.VideoView
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.key.Key
import androidx.compose.ui.input.key.KeyEventType
import androidx.compose.ui.input.key.key
import androidx.compose.ui.input.key.onKeyEvent
import androidx.compose.ui.input.key.type
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import dev.ferry.app.transfer.Storage
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext

/** A file Ferry saved in Downloads/Ferry on this TV. */
data class LocalFile(val uri: Uri, val name: String, val size: Long, val mime: String, val modified: Long) {
    val kind get() = fileKind(name, mime)
}

object Library {
    /** Files this app received. Since Android 10 an app sees only its own files in Downloads, which is exactly this set. */
    fun load(ctx: Context): List<LocalFile> = runCatching {
        val c = MediaStore.Downloads.EXTERNAL_CONTENT_URI
        val proj = arrayOf(MediaStore.MediaColumns._ID, MediaStore.MediaColumns.DISPLAY_NAME, MediaStore.MediaColumns.SIZE,
            MediaStore.MediaColumns.MIME_TYPE, MediaStore.MediaColumns.DATE_MODIFIED)
        ctx.contentResolver.query(c, proj, "${MediaStore.MediaColumns.RELATIVE_PATH} LIKE ?", arrayOf("${Environment.DIRECTORY_DOWNLOADS}/Ferry/%"),
            "${MediaStore.MediaColumns.DATE_MODIFIED} DESC")?.use { cur ->
            buildList {
                while (cur.moveToNext()) {
                    val name = cur.getString(1) ?: "file"
                    add(LocalFile(ContentUris.withAppendedId(c, cur.getLong(0)), name, cur.getLong(2), cur.getString(3) ?: Storage.mimeFor(name), cur.getLong(4) * 1000))
                }
            }
        }.orEmpty()
    }.getOrDefault(emptyList())

    /** Only works for files this install created; after a reinstall Android no longer counts them as ours. */
    fun delete(ctx: Context, f: LocalFile): Boolean = runCatching { ctx.contentResolver.delete(f.uri, null, null) > 0 }.getOrDefault(false)

    /** Hands the file to another app (PDF viewer, package installer for APKs, …). */
    fun openExternally(ctx: Context, uri: Uri, name: String, mime: String): Boolean {
        val type = if (fileKind(name, mime) == "apk") "application/vnd.android.package-archive" else mime.ifEmpty { Storage.mimeFor(name) }
        return try {
            ctx.startActivity(Intent(Intent.ACTION_VIEW).setDataAndType(uri, type)
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_ACTIVITY_NEW_TASK))
            true
        } catch (_: ActivityNotFoundException) {
            false
        }
    }
}

/** Opens a file: photos and videos in the built-in viewer, everything else in another app. */
fun openFile(ctx: Context, f: LocalFile, showViewer: (LocalFile) -> Unit) {
    when (f.kind) {
        "image", "video", "audio" -> showViewer(f)
        else -> if (!Library.openExternally(ctx, f.uri, f.name, f.mime)) toast(ctx, "No app on this TV can open “${f.name}”. Send it to a phone or computer instead.")
    }
}

/** Full-screen viewer. Photos: ◀ ▶ browse. Video and music: OK plays/pauses, ◀ ▶ seek 10 s. Back closes. */
@Composable
fun MediaViewer(files: List<LocalFile>, start: LocalFile, onClose: () -> Unit) {
    val photos = remember(files) { files.filter { it.kind == "image" } }
    var current by remember { mutableStateOf(start) }
    Dialog(onClose, DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false)) {
        Box(Modifier.fillMaxSize().background(Color.Black)) {
            if (current.kind == "image") PhotoView(current, photos) { current = it }
            else PlayerView(current)
        }
    }
}

@Composable
private fun PhotoView(f: LocalFile, photos: List<LocalFile>, onMove: (LocalFile) -> Unit) {
    val ctx = LocalContext.current
    val bmp by produceState<ImageBitmap?>(null, f.uri) {
        value = withContext(Dispatchers.IO) {
            runCatching {
                ImageDecoder.decodeBitmap(ImageDecoder.createSource(ctx.contentResolver, f.uri)) { d, info, _ ->
                    // Decode at screen size: a 50 MP photo at full size would run the TV out of memory.
                    val scale = minOf(1f, 1920f / info.size.width, 1080f / info.size.height)
                    d.setTargetSize((info.size.width * scale).toInt().coerceAtLeast(1), (info.size.height * scale).toInt().coerceAtLeast(1))
                }.asImageBitmap()
            }.getOrNull()
        }
    }
    val i = photos.indexOf(f)
    Box(
        Modifier.fillMaxSize().focusRequester(rememberInitialFocus()).focusable().onKeyEvent { e ->
            if (e.type != KeyEventType.KeyDown) return@onKeyEvent false
            when (e.key) {
                Key.DirectionRight -> { photos.getOrNull(i + 1)?.let(onMove); true }
                Key.DirectionLeft -> { photos.getOrNull(i - 1)?.let(onMove); true }
                else -> false
            }
        },
        contentAlignment = Alignment.Center,
    ) {
        bmp?.let { Image(it, f.name, Modifier.fillMaxSize(), contentScale = ContentScale.Fit) }
            ?: Text("Loading…", color = Tv.muted, style = MaterialTheme.typography.titleLarge)
        Caption(f.name, if (photos.size > 1) "${i + 1} of ${photos.size} · ◀ ▶ to browse · Back to close" else "Back to close")
    }
}

@Composable
private fun PlayerView(f: LocalFile) {
    var view by remember { mutableStateOf<VideoView?>(null) }
    var player by remember { mutableStateOf<MediaPlayer?>(null) }
    var playing by remember { mutableStateOf(true) }
    var pos by remember { mutableLongStateOf(0L) }
    var dur by remember { mutableLongStateOf(0L) }
    var shownAt by remember { mutableLongStateOf(System.currentTimeMillis()) }
    var tick by remember { mutableIntStateOf(0) }
    LaunchedEffect(Unit) {
        while (true) {
            view?.let { pos = it.currentPosition.toLong(); dur = it.duration.toLong().coerceAtLeast(0); playing = it.isPlaying }
            tick++
            delay(500)
        }
    }
    // SEEK_CLOSEST lands exactly on the target; VideoView's own seekTo snaps back to the previous keyframe (can be 10 s off).
    fun seek(ms: Int) = view?.let { v ->
        val to = (v.currentPosition + ms).coerceIn(0, v.duration.coerceAtLeast(0))
        player?.seekTo(to.toLong(), MediaPlayer.SEEK_CLOSEST) ?: v.seekTo(to)
    }
    Box(
        Modifier.fillMaxSize().focusRequester(rememberInitialFocus()).focusable().onKeyEvent { e ->
            if (e.type != KeyEventType.KeyDown) return@onKeyEvent false
            shownAt = System.currentTimeMillis()
            val v = view ?: return@onKeyEvent false
            when (e.key) {
                Key.DirectionCenter, Key.Enter, Key.MediaPlayPause, Key.Spacebar -> { if (v.isPlaying) v.pause() else v.start(); true }
                Key.MediaPlay -> { v.start(); true }
                Key.MediaPause -> { v.pause(); true }
                Key.DirectionRight, Key.MediaFastForward -> { seek(10_000); true }
                Key.DirectionLeft, Key.MediaRewind -> { seek(-10_000); true }
                else -> false
            }
        },
    ) {
        AndroidView({ c ->
            VideoView(c).apply {
                setVideoURI(f.uri)
                setOnPreparedListener { mp -> player = mp; start() }
                view = this
            }
        }, Modifier.fillMaxSize(), onRelease = { player = null; it.stopPlayback() })
        // Controls show after any key press and fade after 4 seconds of playback; always visible while paused.
        if (!playing || System.currentTimeMillis() - shownAt < 4000 || tick < 8) {
            Column(Modifier.align(Alignment.BottomCenter).fillMaxWidth()
                .background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.85f)))).padding(horizontal = 56.dp, vertical = 36.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(if (playing) Icons.Rounded.Pause else Icons.Rounded.PlayArrow, null, Modifier.size(36.dp), tint = Color.White)
                    Spacer(Modifier.width(14.dp))
                    Text(f.name, Modifier.weight(1f), style = MaterialTheme.typography.titleLarge, color = Color.White, maxLines = 1)
                    Text("${clock(pos)} / ${clock(dur)}", style = MaterialTheme.typography.titleMedium, color = Color.White)
                }
                Bar(if (dur > 0) pos.toFloat() / dur else 0f, Modifier.padding(top = 14.dp), height = 6.dp)
                Text("OK play/pause · ◀ ▶ skip 10 s · Back to close", Modifier.padding(top = 10.dp), style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
            }
        }
    }
}

@Composable
private fun androidx.compose.foundation.layout.BoxScope.Caption(title: String, hint: String) {
    Column(Modifier.align(Alignment.BottomStart).fillMaxWidth()
        .background(Brush.verticalGradient(listOf(Color.Transparent, Color.Black.copy(alpha = 0.8f)))).padding(horizontal = 56.dp, vertical = 32.dp)) {
        Text(title, style = MaterialTheme.typography.titleLarge, color = Color.White, maxLines = 1)
        Text(hint, style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
    }
}

private fun clock(ms: Long): String {
    val s = ms / 1000
    return if (s >= 3600) "%d:%02d:%02d".format(s / 3600, s / 60 % 60, s % 60) else "%d:%02d".format(s / 60, s % 60)
}
