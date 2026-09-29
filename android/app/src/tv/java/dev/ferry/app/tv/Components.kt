package dev.ferry.app.tv

import android.content.Context
import android.net.Uri
import android.util.Size
import android.widget.Toast
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.focusable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.InsertDriveFile
import androidx.compose.material.icons.rounded.Android
import androidx.compose.material.icons.rounded.Archive
import androidx.compose.material.icons.rounded.AudioFile
import androidx.compose.material.icons.rounded.Computer
import androidx.compose.material.icons.rounded.Description
import androidx.compose.material.icons.rounded.Folder
import androidx.compose.material.icons.rounded.Image
import androidx.compose.material.icons.rounded.PictureAsPdf
import androidx.compose.material.icons.rounded.Smartphone
import androidx.compose.material.icons.rounded.Tv
import androidx.compose.material.icons.rounded.VideoFile
import androidx.compose.material3.OutlinedTextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.tv.material3.Border
import androidx.tv.material3.Button
import androidx.tv.material3.ButtonDefaults
import androidx.tv.material3.ClickableSurfaceDefaults
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.OutlinedButton
import androidx.tv.material3.Surface
import androidx.tv.material3.SurfaceDefaults
import androidx.tv.material3.Text
import dev.ferry.app.ui.qrBitmap
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import kotlin.math.abs

/** Screen frame: title row plus content, inside the TV overscan-safe area. */
@Composable
fun Page(title: String, subtitle: String? = null, actions: @Composable RowScope.() -> Unit = {}, content: @Composable ColumnScope.() -> Unit) {
    Column(Modifier.fillMaxSize().padding(start = 24.dp, end = 48.dp, top = 24.dp)) {
        Row(Modifier.fillMaxWidth().padding(bottom = 14.dp), verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text(title, style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Bold)
                if (subtitle != null) Text(subtitle, style = MaterialTheme.typography.bodyMedium, color = Tv.muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp), content = actions)
        }
        content()
    }
}

@Composable
fun SectionTitle(text: String, modifier: Modifier = Modifier) {
    Text(text, modifier.padding(top = 4.dp, bottom = 8.dp), style = MaterialTheme.typography.titleMedium, color = Tv.muted)
}

/** A focusable card: grows and gets a white outline when the remote lands on it. */
@Composable
fun FocusCard(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    radius: Dp = 14.dp,
    color: Color = Tv.surface,
    focusedColor: Color = Tv.surfaceHi,
    onLongClick: (() -> Unit)? = null,
    content: @Composable BoxScope.() -> Unit,
) {
    val shape = RoundedCornerShape(radius)
    Surface(
        onClick = onClick,
        onLongClick = onLongClick,
        modifier = modifier,
        shape = ClickableSurfaceDefaults.shape(shape),
        colors = ClickableSurfaceDefaults.colors(containerColor = color, contentColor = Tv.text, focusedContainerColor = focusedColor, focusedContentColor = Tv.text),
        scale = ClickableSurfaceDefaults.scale(focusedScale = 1.05f),
        border = ClickableSurfaceDefaults.border(focusedBorder = Border(BorderStroke(2.dp, Color.White), shape = shape)),
        content = content,
    )
}

/** A static (non-focusable) block of content. */
@Composable
fun Panel(modifier: Modifier = Modifier, color: Color = Tv.surface, padding: Dp = 18.dp, content: @Composable ColumnScope.() -> Unit) {
    Surface(modifier, shape = RoundedCornerShape(18.dp), colors = SurfaceDefaults.colors(containerColor = color, contentColor = Tv.text)) {
        Column(Modifier.padding(padding), content = content)
    }
}

@Composable
fun Action(text: String, icon: ImageVector? = null, modifier: Modifier = Modifier, primary: Boolean = true, enabled: Boolean = true, loading: Boolean = false, onClick: () -> Unit) {
    val inner: @Composable RowScope.() -> Unit = {
        if (loading) {
            androidx.compose.material3.CircularProgressIndicator(Modifier.size(ButtonDefaults.IconSize), strokeWidth = 2.dp)
            Spacer(Modifier.width(ButtonDefaults.IconSpacing))
        } else if (icon != null) {
            Icon(icon, null, Modifier.size(ButtonDefaults.IconSize))
            Spacer(Modifier.width(ButtonDefaults.IconSpacing))
        }
        Text(text, style = MaterialTheme.typography.labelLarge)
    }
    val padding = PaddingValues(horizontal = 18.dp, vertical = 8.dp)
    if (primary) Button(onClick, modifier, enabled = enabled, contentPadding = padding, content = inner)
    else OutlinedButton(onClick, modifier, enabled = enabled, contentPadding = padding, content = inner)
}

@Composable
fun Pill(text: String, color: Color = Tv.muted, icon: ImageVector? = null) {
    Row(Modifier.clip(RoundedCornerShape(50)).background(color.copy(alpha = 0.16f)).padding(horizontal = 10.dp, vertical = 4.dp),
        verticalAlignment = Alignment.CenterVertically) {
        if (icon != null) {
            Icon(icon, null, Modifier.size(14.dp), tint = color)
            Spacer(Modifier.width(4.dp))
        }
        Text(text, style = MaterialTheme.typography.labelMedium, color = color, maxLines = 1)
    }
}

@Composable
fun Bar(value: Float, modifier: Modifier = Modifier, height: Dp = 6.dp) {
    Box(modifier.fillMaxWidth().height(height).clip(RoundedCornerShape(50)).background(Tv.surfaceHi)) {
        Box(Modifier.fillMaxWidth(value.coerceIn(0f, 1f)).fillMaxHeight().clip(RoundedCornerShape(50)).background(Tv.brand))
    }
}

@Composable
fun Empty(icon: ImageVector, title: String, text: String, modifier: Modifier = Modifier) {
    // Focusable so the remote can always leave the side drawer, even on a screen with nothing else to select.
    Column(modifier.fillMaxWidth().focusable().padding(vertical = 24.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        Box(Modifier.size(64.dp).clip(CircleShape).background(Tv.surfaceHi), contentAlignment = Alignment.Center) {
            Icon(icon, null, Modifier.size(32.dp), tint = Tv.primary)
        }
        Spacer(Modifier.height(14.dp))
        Text(title, style = MaterialTheme.typography.titleLarge)
        Text(text, Modifier.widthIn(max = 460.dp).padding(top = 6.dp), style = MaterialTheme.typography.bodyMedium, color = Tv.muted,
            textAlign = androidx.compose.ui.text.style.TextAlign.Center)
    }
}

private val avatarPalette = listOf(
    Color(0xFF2456F5) to Color(0xFF6B8CFF), Color(0xFF7A3DF0) to Color(0xFFB08BFF), Color(0xFF0E9F8E) to Color(0xFF46D3B8),
    Color(0xFFE0612B) to Color(0xFFFFA36B), Color(0xFFD1356B) to Color(0xFFFF7FA8), Color(0xFF1D8FD6) to Color(0xFF6CCBFF),
    Color(0xFF3F9E3B) to Color(0xFF8FD67A), Color(0xFF9A6A12) to Color(0xFFE6B84E),
)

@Composable
fun Avatar(key: String, type: String, size: Dp) {
    val (a, b) = avatarPalette[abs(key.hashCode()) % avatarPalette.size]
    val icon = when (type) {
        "tv" -> Icons.Rounded.Tv
        "desktop", "laptop", "server", "headless", "web" -> Icons.Rounded.Computer
        else -> Icons.Rounded.Smartphone
    }
    Box(Modifier.size(size).clip(CircleShape).background(Brush.linearGradient(listOf(a, b))), contentAlignment = Alignment.Center) {
        Icon(icon, null, Modifier.size(size * 0.46f), tint = Color.White)
    }
}

@Composable
fun QrCode(value: String, size: Dp) {
    val bmp = remember(value) { qrBitmap(value, 600).asImageBitmap() }
    Box(Modifier.clip(RoundedCornerShape(14.dp)).background(Color.White).padding(10.dp)) {
        Image(bmp, "QR code", Modifier.size(size))
    }
}

/** Pairing code as big separate characters, readable from the sofa. */
@Composable
fun CodeBoxes(code: String) {
    Row(horizontalArrangement = Arrangement.spacedBy(5.dp), verticalAlignment = Alignment.CenterVertically) {
        code.forEach { c ->
            if (c == '-') Text("–", style = MaterialTheme.typography.titleLarge, color = Tv.muted)
            else Box(Modifier.width(30.dp).clip(RoundedCornerShape(7.dp)).background(Tv.surfaceHi).padding(vertical = 5.dp), contentAlignment = Alignment.Center) {
                Text(c.toString(), fontFamily = FontFamily.Monospace, fontSize = 20.sp, fontWeight = FontWeight.Bold)
            }
        }
    }
}

fun fileKind(name: String, mime: String): String {
    val ext = name.substringAfterLast('.', "").lowercase()
    return when {
        mime.startsWith("video/") || ext in setOf("mp4", "mkv", "mov", "webm", "avi", "m4v") -> "video"
        mime.startsWith("image/") || ext in setOf("jpg", "jpeg", "png", "gif", "webp", "heic", "bmp") -> "image"
        mime.startsWith("audio/") || ext in setOf("mp3", "m4a", "wav", "flac", "ogg", "aac") -> "audio"
        ext == "apk" || mime == "application/vnd.android.package-archive" -> "apk"
        else -> "other"
    }
}

fun fileIcon(name: String, mime: String, folder: Boolean = false): Pair<ImageVector, Color> {
    if (folder) return Icons.Rounded.Folder to Color(0xFF6B8CFF)
    val ext = name.substringAfterLast('.', "").lowercase()
    return when (fileKind(name, mime)) {
        "video" -> Icons.Rounded.VideoFile to Color(0xFFB08BFF)
        "image" -> Icons.Rounded.Image to Color(0xFFFFA36B)
        "audio" -> Icons.Rounded.AudioFile to Color(0xFF46D3B8)
        "apk" -> Icons.Rounded.Android to Color(0xFF8FD67A)
        else -> when {
            mime == "application/pdf" || ext == "pdf" -> Icons.Rounded.PictureAsPdf to Color(0xFFFF7A7A)
            ext in setOf("zip", "rar", "7z", "gz", "tar") -> Icons.Rounded.Archive to Color(0xFFE6B84E)
            mime.startsWith("text/") || ext in setOf("doc", "docx", "txt", "md", "odt", "xls", "xlsx", "ppt", "pptx", "csv") -> Icons.Rounded.Description to Color(0xFF6CCBFF)
            else -> Icons.AutoMirrored.Rounded.InsertDriveFile to Tv.muted
        }
    }
}

/** Thumbnail for photos and videos, or a big file-type icon. */
@Composable
fun Thumb(uri: Uri?, name: String, mime: String, modifier: Modifier) {
    val ctx = LocalContext.current
    val kind = fileKind(name, mime)
    val bmp by produceState<ImageBitmap?>(null, uri) {
        if (uri != null && (kind == "image" || kind == "video")) value = withContext(Dispatchers.IO) {
            runCatching { ctx.contentResolver.loadThumbnail(uri, Size(480, 270), null).asImageBitmap() }.getOrNull()
        }
    }
    Box(modifier.background(Tv.surfaceHi), contentAlignment = Alignment.Center) {
        val b = bmp
        if (b != null) Image(b, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop)
        else {
            val (icon, tint) = fileIcon(name, mime)
            Icon(icon, null, Modifier.size(36.dp), tint = tint)
        }
    }
}

/** Requests focus once the composable is on screen (e.g. the main action of a dialog). */
@Composable
fun rememberInitialFocus(key: Any? = Unit): FocusRequester {
    val fr = remember { FocusRequester() }
    LaunchedEffect(key) {
        delay(80)
        runCatching { fr.requestFocus() }
    }
    return fr
}

/** Centered dialog card for the 10-foot screen. */
@Composable
fun TvDialog(onDismiss: () -> Unit, width: Dp = 520.dp, content: @Composable ColumnScope.() -> Unit) {
    Dialog(onDismiss, DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.width(width), shape = RoundedCornerShape(20.dp), colors = SurfaceDefaults.colors(containerColor = Tv.surface, contentColor = Tv.text)) {
            Column(Modifier.padding(24.dp), content = content)
        }
    }
}

/** Inline spinner + label for screens that are fetching something. */
@Composable
fun Loading(text: String, modifier: Modifier = Modifier) {
    Row(modifier.padding(vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        androidx.compose.material3.CircularProgressIndicator(Modifier.size(28.dp), color = Tv.primary, strokeWidth = 3.dp)
        Spacer(Modifier.width(14.dp))
        Text(text, style = MaterialTheme.typography.titleMedium, color = Tv.muted)
    }
}

/** Blocking spinner card for one-shot actions (connect, add server). */
@Composable
fun WorkingDialog(text: String) = TvDialog({}, width = 360.dp) { Loading(text) }

@Composable
fun ConfirmDialog(title: String, text: String, confirm: String, onDismiss: () -> Unit, onConfirm: () -> Unit) {
    TvDialog(onDismiss) {
        Text(title, style = MaterialTheme.typography.titleLarge)
        Text(text, Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
        Row(Modifier.fillMaxWidth().padding(top = 20.dp), horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.End)) {
            Action("Cancel", primary = false, modifier = Modifier.focusRequester(rememberInitialFocus()), onClick = onDismiss)
            Action(confirm, onClick = onConfirm)
        }
    }
}

/** Text entry: the field opens the TV's on-screen keyboard; "Done" on the keyboard submits. */
@Composable
fun InputDialog(
    title: String,
    label: String,
    initial: String = "",
    hint: String? = null,
    confirm: String = "Save",
    keyboard: KeyboardType = KeyboardType.Text,
    password: Boolean = false,
    filter: (String) -> String = { it },
    onDismiss: () -> Unit,
    onDone: (String) -> Unit,
) {
    var v by remember { mutableStateOf(initial) }
    TvDialog(onDismiss) {
        Text(title, style = MaterialTheme.typography.titleLarge)
        if (hint != null) Text(hint, Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
        OutlinedTextField(
            v, { v = filter(it) }, Modifier.fillMaxWidth().padding(top = 14.dp).focusRequester(rememberInitialFocus()),
            label = { androidx.compose.material3.Text(label) }, singleLine = true,
            visualTransformation = if (password) PasswordVisualTransformation() else VisualTransformation.None,
            keyboardOptions = KeyboardOptions(keyboardType = keyboard, imeAction = ImeAction.Done),
            keyboardActions = KeyboardActions(onDone = { if (v.isNotBlank()) onDone(v.trim()) }),
        )
        Row(Modifier.fillMaxWidth().padding(top = 20.dp), horizontalArrangement = Arrangement.spacedBy(12.dp, Alignment.End)) {
            Action("Cancel", primary = false, onClick = onDismiss)
            Action(confirm, enabled = v.isNotBlank()) { onDone(v.trim()) }
        }
    }
}

fun toast(ctx: Context, text: String) = Toast.makeText(ctx, text, Toast.LENGTH_LONG).show()
