package dev.ferry.app.ui

import android.content.ActivityNotFoundException
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import androidx.activity.result.ActivityResultLauncher
import android.content.Intent
import android.graphics.Bitmap
import android.net.Uri
import android.util.Size
import android.widget.Toast
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.LinearEasing
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.animation.expandHorizontally
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkHorizontally
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.InsertDriveFile
import androidx.compose.material.icons.automirrored.rounded.KeyboardArrowRight
import androidx.compose.material.icons.rounded.Archive
import androidx.compose.material.icons.rounded.AudioFile
import androidx.compose.material.icons.rounded.Computer
import androidx.compose.material.icons.rounded.Description
import androidx.compose.material.icons.rounded.Folder
import androidx.compose.material.icons.rounded.Image
import androidx.compose.material.icons.rounded.PictureAsPdf
import androidx.compose.material.icons.rounded.Smartphone
import androidx.compose.material.icons.rounded.Tablet
import androidx.compose.material.icons.rounded.VideoFile
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.scale
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlin.math.abs

// ============================================================================================
// Ferry design system for Compose: large headers on the page background, grouped rounded
// cards, tinted icon badges, device avatars and gentle motion. Screens compose these pieces.
// ============================================================================================

val BrandGradient = Brush.linearGradient(listOf(Color(0xFF2456F5), Color(0xFF6B4DFF), Color(0xFF9B5CFF)))

/** Screen header: big title that sits on the background (no app-bar slab), optional back + actions. */
@Composable
fun ScreenHeader(title: String, subtitle: String? = null, onBack: (() -> Unit)? = null, actions: @Composable RowScope.() -> Unit = {}) {
    Column(Modifier.fillMaxWidth().padding(start = if (onBack != null) 8.dp else 20.dp, end = 12.dp, top = 8.dp, bottom = 8.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (onBack != null) {
                IconButton(onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, "Back") }
                Spacer(Modifier.width(4.dp))
            }
            Column(Modifier.weight(1f)) {
                Text(title, style = MaterialTheme.typography.headlineMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                if (subtitle != null) Text(subtitle, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            actions()
        }
    }
}

@Composable
fun SectionTitle(text: String, modifier: Modifier = Modifier, action: (@Composable () -> Unit)? = null) {
    Row(modifier.fillMaxWidth().padding(start = 4.dp, top = 22.dp, bottom = 10.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(text, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium, color = MaterialTheme.colorScheme.onSurface)
        action?.invoke()
    }
}

/** Soft rounded surface; the base container for grouped content. */
@Composable
fun Panel(modifier: Modifier = Modifier, padding: Dp = 18.dp, onClick: (() -> Unit)? = null, content: @Composable ColumnScope.() -> Unit) {
    val shape = RoundedCornerShape(22.dp)
    Column(
        modifier.fillMaxWidth().clip(shape).background(MaterialTheme.colorScheme.surface)
            .border(1.dp, MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f), shape)
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(padding),
        content = content,
    )
}

/** Card of list rows separated by inset dividers (settings-style group). */
@Composable
fun GroupCard(modifier: Modifier = Modifier, rows: List<@Composable () -> Unit>) {
    val shape = RoundedCornerShape(22.dp)
    Column(modifier.fillMaxWidth().clip(shape).background(MaterialTheme.colorScheme.surface)
        .border(1.dp, MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f), shape)) {
        rows.forEachIndexed { i, row ->
            row()
            if (i < rows.lastIndex) HorizontalDivider(Modifier.padding(start = 70.dp), color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f))
        }
    }
}

/** One list row: tinted icon badge, title/subtitle, trailing content or chevron. */
@Composable
fun RowItem(
    title: String,
    subtitle: String? = null,
    icon: ImageVector? = null,
    tint: Color = MaterialTheme.colorScheme.primary,
    leading: (@Composable () -> Unit)? = null,
    onClick: (() -> Unit)? = null,
    chevron: Boolean = onClick != null,
    trailing: (@Composable () -> Unit)? = null,
) {
    Row(
        Modifier.fillMaxWidth().heightIn(min = 64.dp).then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        when {
            leading != null -> leading()
            icon != null -> IconBadge(icon, tint)
        }
        if (leading != null || icon != null) Spacer(Modifier.width(14.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (!subtitle.isNullOrEmpty()) Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 2, overflow = TextOverflow.Ellipsis)
        }
        if (trailing != null) {
            Spacer(Modifier.width(8.dp)); trailing()
        } else if (chevron) {
            Icon(Icons.AutoMirrored.Rounded.KeyboardArrowRight, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
fun IconBadge(icon: ImageVector, tint: Color = MaterialTheme.colorScheme.primary, size: Dp = 40.dp, filled: Boolean = false) {
    Box(
        Modifier.size(size).clip(RoundedCornerShape(size * 0.32f)).background(if (filled) tint else tint.copy(alpha = 0.13f)),
        contentAlignment = Alignment.Center,
    ) { Icon(icon, null, Modifier.size(size * 0.52f), tint = if (filled) Color.White else tint) }
}

/** Pill-shaped status label. */
@Composable
fun Chip(text: String, color: Color = MaterialTheme.colorScheme.onSurfaceVariant, bg: Color = MaterialTheme.colorScheme.surfaceVariant, icon: ImageVector? = null) {
    Row(
        Modifier.clip(RoundedCornerShape(50)).background(bg).padding(horizontal = 10.dp, vertical = 5.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) {
            Icon(icon, null, Modifier.size(14.dp), tint = color); Spacer(Modifier.width(5.dp))
        }
        Text(text, style = MaterialTheme.typography.labelMedium, color = color, maxLines = 1, fontWeight = FontWeight.SemiBold)
    }
}

/** Deterministic friendly colour per device, like LocalSend's avatars. */
fun avatarColors(key: String): Pair<Color, Color> {
    val palette = listOf(
        Color(0xFF2456F5) to Color(0xFF6B8CFF), Color(0xFF7A3DF0) to Color(0xFFB08BFF), Color(0xFF0E9F8E) to Color(0xFF46D3B8),
        Color(0xFFE0612B) to Color(0xFFFFA36B), Color(0xFFD1356B) to Color(0xFFFF7FA8), Color(0xFF1D8FD6) to Color(0xFF6CCBFF),
        Color(0xFF3F9E3B) to Color(0xFF8FD67A), Color(0xFF9A6A12) to Color(0xFFE6B84E),
    )
    return palette[abs(key.hashCode()) % palette.size]
}

@Composable
fun DeviceAvatar(key: String, type: String = "mobile", size: Dp = 52.dp, modifier: Modifier = Modifier) {
    val (a, b) = avatarColors(key)
    val icon = when (type) {
        "desktop", "laptop", "server", "headless", "web" -> Icons.Rounded.Computer
        "tablet" -> Icons.Rounded.Tablet
        else -> Icons.Rounded.Smartphone
    }
    Box(modifier.size(size).clip(CircleShape).background(Brush.linearGradient(listOf(a, b))), contentAlignment = Alignment.Center) {
        Icon(icon, null, Modifier.size(size * 0.46f), tint = Color.White)
    }
}

/** Expanding rings behind content — used while receiving or searching. */
@Composable
fun RadarPulse(active: Boolean, color: Color = MaterialTheme.colorScheme.primary, size: Dp = 220.dp, content: @Composable BoxScope.() -> Unit) {
    val t = rememberInfiniteTransition(label = "radar")
    val phase by t.animateFloat(0f, 1f, infiniteRepeatable(tween(2600, easing = LinearEasing), RepeatMode.Restart), label = "phase")
    Box(Modifier.size(size), contentAlignment = Alignment.Center) {
        if (active) Canvas(Modifier.fillMaxSize()) {
            for (i in 0 until 3) {
                val p = (phase + i / 3f) % 1f
                drawCircle(color.copy(alpha = (1f - p) * 0.22f), radius = this.size.minDimension / 2 * (0.3f + 0.7f * p))
            }
        } else Canvas(Modifier.fillMaxSize()) {
            drawCircle(color.copy(alpha = 0.06f), radius = this.size.minDimension / 2 * 0.62f)
        }
        content()
    }
}

/** Circular progress with centred percentage. */
@Composable
fun ProgressRing(value: Float, size: Dp = 48.dp, stroke: Dp = 5.dp, color: Color = MaterialTheme.colorScheme.primary) {
    val v by animateFloatAsState(value.coerceIn(0f, 1f), tween(300), label = "ring")
    val track = MaterialTheme.colorScheme.outlineVariant
    Box(Modifier.size(size), contentAlignment = Alignment.Center) {
        Canvas(Modifier.fillMaxSize()) {
            val s = stroke.toPx()
            drawArc(track, 0f, 360f, false, style = Stroke(s), topLeft = Offset(s / 2, s / 2), size = androidx.compose.ui.geometry.Size(this.size.width - s, this.size.height - s))
            drawArc(color, -90f, 360f * v, false, style = Stroke(s, cap = StrokeCap.Round), topLeft = Offset(s / 2, s / 2),
                size = androidx.compose.ui.geometry.Size(this.size.width - s, this.size.height - s))
        }
        Text("${(v * 100).toInt()}", style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.Bold)
    }
}

@Composable
fun Progress(value: Float, modifier: Modifier = Modifier, height: Dp = 8.dp) {
    val v by animateFloatAsState(value.coerceIn(0f, 1f), tween(300), label = "bar")
    Box(modifier.fillMaxWidth().height(height).clip(RoundedCornerShape(50)).background(MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.7f))) {
        Box(Modifier.fillMaxWidth(v).fillMaxHeight().clip(RoundedCornerShape(50)).background(BrandGradient))
    }
}

/** Custom segmented control (pill track with sliding selection). */
@Composable
fun Segmented(options: List<String>, selected: Int, onSelect: (Int) -> Unit, modifier: Modifier = Modifier) {
    Row(modifier.fillMaxWidth().clip(RoundedCornerShape(50)).background(MaterialTheme.colorScheme.surfaceVariant).padding(4.dp)) {
        options.forEachIndexed { i, label ->
            val on = i == selected
            val bg by animateColorAsState(if (on) MaterialTheme.colorScheme.surface else Color.Transparent, label = "seg")
            Box(
                Modifier.weight(1f).clip(RoundedCornerShape(50)).background(bg).clickable { onSelect(i) }.padding(vertical = 10.dp),
                contentAlignment = Alignment.Center,
            ) {
                Text(label, style = MaterialTheme.typography.labelLarge,
                    color = if (on) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

/** Bottom space tab screens keep free so the last item can scroll above the floating nav bar. */
val NavBarSpace = 110.dp

/** Floating pill navigation bar with an expanding label for the selected item. */
data class NavTab(val route: String, val label: String, val icon: ImageVector, val badge: Int = 0)

@Composable
fun FloatingNavBar(tabs: List<NavTab>, selected: String, onSelect: (String) -> Unit) {
    Box(Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 20.dp, vertical = 10.dp), contentAlignment = Alignment.Center) {
        Surface(
            shape = RoundedCornerShape(50),
            color = MaterialTheme.colorScheme.surface,
            shadowElevation = 12.dp,
            tonalElevation = 0.dp,
            modifier = Modifier.border(1.dp, MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f), RoundedCornerShape(50)),
        ) {
            Row(Modifier.padding(6.dp), horizontalArrangement = Arrangement.spacedBy(2.dp), verticalAlignment = Alignment.CenterVertically) {
                tabs.forEach { t ->
                    val on = t.route == selected
                    val bg by animateColorAsState(if (on) MaterialTheme.colorScheme.primary else Color.Transparent, label = "nav")
                    Row(
                        Modifier.clip(RoundedCornerShape(50)).background(bg).clickable { onSelect(t.route) }
                            .padding(horizontal = 16.dp, vertical = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Box {
                            Icon(t.icon, t.label, tint = if (on) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurfaceVariant)
                            if (t.badge > 0) Box(Modifier.align(Alignment.TopEnd).size(9.dp).clip(CircleShape).background(MaterialTheme.colorScheme.error))
                        }
                        AnimatedVisibility(on, enter = fadeIn() + expandHorizontally(), exit = fadeOut() + shrinkHorizontally()) {
                            Text(t.label, Modifier.padding(start = 8.dp), color = MaterialTheme.colorScheme.onPrimary,
                                style = MaterialTheme.typography.labelLarge, maxLines = 1)
                        }
                    }
                }
            }
        }
    }
}

/** Brand mark: gradient tile with the tray-and-arrow glyph. */
@Composable
fun BrandMark(size: Dp = 36.dp) {
    Box(Modifier.size(size).clip(RoundedCornerShape(size * 0.3f)).background(BrandGradient), contentAlignment = Alignment.Center) {
        Canvas(Modifier.size(size * 0.62f)) {
            val w = this.size.width
            val s = w * 0.11f
            val c = Color.White
            val tray = androidx.compose.ui.graphics.Path().apply {
                moveTo(w * 0.1f, w * 0.52f); lineTo(w * 0.9f, w * 0.52f); lineTo(w * 0.78f, w * 0.9f); lineTo(w * 0.22f, w * 0.9f); close()
            }
            drawPath(tray, c, style = Stroke(s, join = androidx.compose.ui.graphics.StrokeJoin.Round))
            drawLine(c, Offset(w / 2, w * 0.08f), Offset(w / 2, w * 0.44f), s, StrokeCap.Round)
            drawLine(c, Offset(w * 0.33f, w * 0.24f), Offset(w / 2, w * 0.08f), s, StrokeCap.Round)
            drawLine(c, Offset(w * 0.67f, w * 0.24f), Offset(w / 2, w * 0.08f), s, StrokeCap.Round)
        }
    }
}

@Composable
fun EmptyState(icon: ImageVector, title: String, text: String, action: (@Composable () -> Unit)? = null) {
    Column(Modifier.fillMaxWidth().padding(vertical = 36.dp, horizontal = 28.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        RadarPulse(false, size = 120.dp) {
            Box(Modifier.size(64.dp).clip(RoundedCornerShape(22.dp)).background(MaterialTheme.colorScheme.primaryContainer), contentAlignment = Alignment.Center) {
                Icon(icon, null, Modifier.size(30.dp), tint = MaterialTheme.colorScheme.primary)
            }
        }
        Text(title, style = MaterialTheme.typography.titleLarge, textAlign = TextAlign.Center)
        Spacer(Modifier.height(6.dp))
        Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
        if (action != null) {
            Spacer(Modifier.height(18.dp)); action()
        }
    }
}

fun iconFor(name: String, mime: String = "", folder: Boolean = false): Pair<ImageVector, Color> {
    if (folder) return Icons.Rounded.Folder to Color(0xFF2456F5)
    val ext = name.substringAfterLast('.', "").lowercase()
    return when {
        mime.startsWith("image/") || ext in setOf("jpg", "jpeg", "png", "gif", "webp", "heic") -> Icons.Rounded.Image to Color(0xFFE0612B)
        mime.startsWith("video/") || ext in setOf("mp4", "mkv", "mov", "webm") -> Icons.Rounded.VideoFile to Color(0xFF7A3DF0)
        mime.startsWith("audio/") || ext in setOf("mp3", "m4a", "wav", "flac", "ogg") -> Icons.Rounded.AudioFile to Color(0xFF0E9F8E)
        mime == "application/pdf" || ext == "pdf" -> Icons.Rounded.PictureAsPdf to Color(0xFFD1352F)
        ext in setOf("zip", "rar", "7z", "gz", "tar", "apk") -> Icons.Rounded.Archive to Color(0xFFB07A0E)
        mime.startsWith("text/") || ext in setOf("doc", "docx", "txt", "md", "odt", "xls", "xlsx", "ppt", "pptx", "csv") -> Icons.Rounded.Description to Color(0xFF2456F5)
        else -> Icons.AutoMirrored.Rounded.InsertDriveFile to Color(0xFF6B6B74)
    }
}

@Composable
fun FileIcon(name: String, mime: String = "", folder: Boolean = false, size: Dp = 44.dp) {
    val (icon, color) = iconFor(name, mime, folder)
    IconBadge(icon, color, size)
}

/** Real thumbnail for local images/videos, falling back to the type icon. */
@Composable
fun FileThumb(uri: Uri?, name: String, mime: String, size: Dp = 64.dp) {
    val ctx = LocalContext.current
    val bmp by produceState<Bitmap?>(null, uri) {
        value = if (uri != null && (mime.startsWith("image/") || mime.startsWith("video/"))) withContext(Dispatchers.IO) {
            runCatching { ctx.contentResolver.loadThumbnail(uri, Size(256, 256), null) }.getOrNull()
        } else null
    }
    val b = bmp
    if (b != null) Image(b.asImageBitmap(), null, Modifier.size(size).clip(RoundedCornerShape(size * 0.28f)), contentScale = ContentScale.Crop)
    else FileIcon(name, mime, size = size)
}

@Composable
fun StatusDot(on: Boolean) {
    val t = rememberInfiniteTransition(label = "dot")
    val s by t.animateFloat(1f, if (on) 1.35f else 1f, infiniteRepeatable(tween(900), RepeatMode.Reverse), label = "s")
    Box(Modifier.size(10.dp).scale(s).clip(CircleShape).background(if (on) LocalExtra.current.ok else MaterialTheme.colorScheme.outline))
}

@Composable
fun QrImage(value: String, size: Dp = 200.dp) {
    val bmp = remember(value) { qrBitmap(value, 512) }
    Surface(color = Color.White, shape = RoundedCornerShape(24.dp), shadowElevation = 6.dp) {
        Image(bmp.asImageBitmap(), contentDescription = "QR code", modifier = Modifier.padding(14.dp).size(size))
    }
}

/** Big primary action button (56dp, fully rounded). */
@Composable
fun BigButton(text: String, icon: ImageVector? = null, modifier: Modifier = Modifier, enabled: Boolean = true, tonal: Boolean = false, onClick: () -> Unit) {
    val bg = when {
        !enabled -> MaterialTheme.colorScheme.surfaceVariant
        tonal -> MaterialTheme.colorScheme.primaryContainer
        else -> MaterialTheme.colorScheme.primary
    }
    val fg = when {
        !enabled -> MaterialTheme.colorScheme.onSurfaceVariant
        tonal -> MaterialTheme.colorScheme.primary
        else -> MaterialTheme.colorScheme.onPrimary
    }
    Row(
        modifier.heightIn(min = 56.dp).clip(RoundedCornerShape(50)).background(bg).clickable(enabled = enabled, onClick = onClick).padding(horizontal = 24.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.Center,
    ) {
        if (icon != null) {
            Icon(icon, null, tint = fg, modifier = Modifier.size(20.dp)); Spacer(Modifier.width(10.dp))
        }
        Text(text, color = fg, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
    }
}

/** Small tonal pill action (e.g. "Scan", "Copy"). */
@Composable
fun PillAction(text: String, icon: ImageVector? = null, onClick: () -> Unit) {
    Row(
        Modifier.clip(RoundedCornerShape(50)).background(MaterialTheme.colorScheme.primaryContainer).clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (icon != null) {
            Icon(icon, null, Modifier.size(16.dp), tint = MaterialTheme.colorScheme.primary); Spacer(Modifier.width(6.dp))
        }
        Text(text, style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
    }
}

@Composable
fun KeyValue(k: String, v: String) {
    Row(Modifier.fillMaxWidth().padding(vertical = 6.dp), horizontalArrangement = Arrangement.SpaceBetween) {
        Text(k, color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodyMedium)
        Spacer(Modifier.width(12.dp))
        Text(v, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.End)
    }
}

@Composable
fun TrailingValue(text: String) {
    Text(text, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
}

@Composable
fun Hint(text: String, icon: ImageVector, color: Color = MaterialTheme.colorScheme.primary) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(18.dp)).background(color.copy(alpha = 0.1f)).padding(14.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Icon(icon, null, tint = color, modifier = Modifier.size(20.dp))
        Spacer(Modifier.width(10.dp))
        Text(text, style = MaterialTheme.typography.bodyMedium, color = LocalContentColor.current)
    }
}

fun copyText(ctx: Context, text: String, label: String = "Link") {
    ctx.getSystemService(ClipboardManager::class.java).setPrimaryClip(ClipData.newPlainText(label, text))
    Toast.makeText(ctx, "$label copied", Toast.LENGTH_SHORT).show()
}

fun shareText(ctx: Context, text: String) {
    ctx.startActivity(Intent.createChooser(Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text), "Share link")
        .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
}

/** Opens the system file picker; many Android TVs ship without one. */
fun ActivityResultLauncher<Array<String>>.pickFiles(ctx: Context) {
    val picker = Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*")
    try {
        // Android TV's framework stubs claim the intent but only show a generic "no app" toast.
        val pkg = picker.resolveActivity(ctx.packageManager)?.packageName
        if (pkg == null || pkg == "com.android.tv.frameworkpackagestubs") throw ActivityNotFoundException()
        launch(arrayOf("*/*"))
    } catch (_: ActivityNotFoundException) {
        toast(ctx, "This device has no file picker. Install a file manager, or share files to Ferry from another app.")
    }
}

fun toast(ctx: Context, text: String) = Toast.makeText(ctx, text, Toast.LENGTH_LONG).show()
