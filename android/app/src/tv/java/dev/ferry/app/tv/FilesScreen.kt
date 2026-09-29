package dev.ferry.app.tv

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Send
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.FolderOpen
import androidx.compose.material.icons.rounded.InstallMobile
import androidx.compose.material.icons.rounded.OpenInNew
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import dev.ferry.app.FerryApp
import dev.ferry.app.data.TFile
import dev.ferry.app.ui.SendQueue
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.relativeTime
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import java.util.UUID

private val filters = listOf("All" to "", "Videos" to "video", "Photos" to "image", "Music" to "audio", "Apps" to "apk", "Other" to "other")

@Composable
fun FilesScreen(onViewFile: (LocalFile, List<LocalFile>) -> Unit, onSend: () -> Unit) {
    val ctx = LocalContext.current
    val history by FerryApp.app.history.items.collectAsState()
    var files by remember { mutableStateOf<List<LocalFile>?>(null) }
    var filter by rememberSaveable { mutableStateOf("") }
    var selected by remember { mutableStateOf<LocalFile?>(null) }
    var confirmDelete by remember { mutableStateOf<LocalFile?>(null) }
    var reload by remember { mutableIntStateOf(0) }
    // After the action dialog or the viewer closes, return to the card that opened it.
    val cardFocus = remember { mutableMapOf<String, FocusRequester>() }
    var lastCard by remember { mutableStateOf<String?>(null) }
    val windowFocused = LocalWindowInfo.current.isWindowFocused
    LaunchedEffect(windowFocused, selected) {
        val key = lastCard ?: return@LaunchedEffect
        if (windowFocused && selected == null) {
            delay(80)
            runCatching { cardFocus[key]?.requestFocus() }
        }
    }
    // Reload when a transfer finishes (history grows) or after a delete.
    LaunchedEffect(history.size, reload) { files = withContext(Dispatchers.IO) { Library.load(ctx) } }

    val all = files.orEmpty()
    val shown = if (filter.isEmpty()) all else all.filter { it.kind == filter }

    Page("Files", files?.let { "${it.size} received · ${formatBytes(it.sumOf { f -> f.size })} · Downloads/Ferry" }) {
        Row(Modifier.padding(bottom = 10.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            filters.forEach { (label, kind) ->
                val n = if (kind.isEmpty()) all.size else all.count { it.kind == kind }
                if (kind.isEmpty() || n > 0) FilterPill(if (kind.isEmpty()) label else "$label · $n", filter == kind) { filter = kind }
            }
        }
        when {
            files == null -> Loading("Loading files…")
            shown.isEmpty() -> Empty(Icons.Rounded.FolderOpen, "Nothing here yet",
                "Files sent to this TV are saved in Downloads/Ferry and show up here. Open Receive to see the code to send with.")
            else -> LazyVerticalGrid(GridCells.Adaptive(180.dp), Modifier.fillMaxSize(), contentPadding = PaddingValues(top = 6.dp, bottom = 32.dp, start = 4.dp, end = 4.dp),
                horizontalArrangement = Arrangement.spacedBy(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                items(shown, key = { it.uri.toString() }) { f ->
                    val key = f.uri.toString()
                    FocusCard({ selected = f }, Modifier.focusRequester(cardFocus.getOrPut(key) { FocusRequester() })
                        .onFocusChanged { if (it.hasFocus) lastCard = key }, onLongClick = { selected = f }) {
                        Column {
                            Thumb(f.uri, f.name, f.mime, Modifier.fillMaxWidth().aspectRatio(16f / 9f))
                            Column(Modifier.padding(horizontal = 10.dp, vertical = 8.dp)) {
                                Text(f.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text("${formatBytes(f.size)} · ${relativeTime(f.modified)}", style = MaterialTheme.typography.bodySmall, color = Tv.muted, maxLines = 1)
                            }
                        }
                    }
                }
            }
        }
    }

    selected?.let { f ->
        TvDialog({ selected = null }) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Thumb(f.uri, f.name, f.mime, Modifier.fillMaxWidth(0.35f).aspectRatio(16f / 9f))
                Column(Modifier.padding(start = 14.dp)) {
                    Text(f.name, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    Text("${formatBytes(f.size)} · received ${relativeTime(f.modified)}", style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
                }
            }
            Row(Modifier.fillMaxWidth().padding(top = 20.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                val (label, icon) = when (f.kind) {
                    "video", "audio" -> "Play" to Icons.Rounded.PlayArrow
                    "apk" -> "Install" to Icons.Rounded.InstallMobile
                    else -> "Open" to Icons.Rounded.OpenInNew
                }
                Action(label, icon, Modifier.focusRequester(rememberInitialFocus())) {
                    selected = null
                    openFile(ctx, f) { onViewFile(it, shown) }
                }
                Action("Send to a device", Icons.AutoMirrored.Rounded.Send, primary = false) {
                    selected = null
                    SendQueue.add(listOf(TFile(UUID.randomUUID().toString(), f.name, f.size, f.mime, f.uri)))
                    onSend()
                }
                Action("Delete", Icons.Rounded.Delete, primary = false) { selected = null; confirmDelete = f }
            }
        }
    }
    confirmDelete?.let { f ->
        ConfirmDialog("Delete “${f.name}”?", "It's removed from this TV. This can't be undone.", "Delete", { confirmDelete = null }) {
            confirmDelete = null
            if (Library.delete(ctx, f)) reload++
            else toast(ctx, "Android doesn't let Ferry delete this file (it may be from an earlier install). Remove it in the TV's storage settings.")
        }
    }
}

/** Filter toggle; the selected one is filled. (Compose for TV's FilterChip is still experimental.) */
@Composable
private fun FilterPill(text: String, selected: Boolean, onClick: () -> Unit) {
    FocusCard(onClick, radius = 50.dp, color = if (selected) Tv.primary else Tv.surface, focusedColor = if (selected) Tv.primary else Tv.surfaceHi) {
        Text(text, Modifier.padding(horizontal = 14.dp, vertical = 6.dp), style = MaterialTheme.typography.labelLarge,
            color = if (selected) Tv.onPrimary else Tv.text)
    }
}
