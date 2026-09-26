package dev.ferry.app.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.CallMade
import androidx.compose.material.icons.automirrored.rounded.CallReceived
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.DeleteSweep
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Link
import androidx.compose.material.icons.rounded.MoreVert
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Share
import androidx.compose.material.icons.rounded.SwapVert
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilledTonalIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import dev.ferry.app.FerryApp
import dev.ferry.app.data.Method
import dev.ferry.app.data.TStatus
import dev.ferry.app.data.Transfer
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.formatEta
import dev.ferry.app.util.formatSpeed
import dev.ferry.app.util.relativeTime

@Composable
fun TransfersScreen(@Suppress("UNUSED_PARAMETER") nav: NavHostController) {
    val app = FerryApp.app
    val active by app.transfers.active.collectAsState()
    val history by app.history.items.collectAsState()
    var tab by remember { mutableIntStateOf(0) }
    var linkFor by remember { mutableStateOf<Transfer?>(null) }
    var confirmClear by remember { mutableStateOf(false) }
    LaunchedEffect(active.isEmpty()) { if (active.isEmpty() && history.isNotEmpty()) tab = 1 }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader("Transfers", actions = {
            if (tab == 1 && history.isNotEmpty()) IconButton({ confirmClear = true }) { Icon(Icons.Rounded.DeleteSweep, "Clear history") }
        })
        Segmented(listOf("Active" + if (active.isNotEmpty()) " · ${active.size}" else "", "History"), tab, { tab = it }, Modifier.padding(horizontal = 20.dp))
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 16.dp, bottom = NavBarSpace), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            if (tab == 0) {
                if (active.isEmpty()) item {
                    EmptyState(Icons.Rounded.SwapVert, "Nothing in progress", "Transfers you start — nearby, through your server or as links — show their progress here.")
                }
                items(active, key = { it.id }) { t -> TransferCard(t) }
            } else {
                if (history.isEmpty()) item { EmptyState(Icons.Rounded.SwapVert, "No history yet", "Finished transfers are listed here. Clearing history never deletes files.") }
                else item {
                    GroupCard(rows = history.map { t -> { HistoryRow(t) { linkFor = it } } })
                }
            }
        }
    }
    linkFor?.let { t ->
        LinkOptionsDialog(onDismiss = { linkFor = null }) { exp, max, pw ->
            linkFor = null
            app.transfers.linkable[t.id]?.let { app.transfers.shareAsLink(it, exp, max, pw) }
        }
    }
    if (confirmClear) AlertDialog(
        onDismissRequest = { confirmClear = false },
        icon = { IconBadge(Icons.Rounded.DeleteSweep, size = 52.dp) },
        title = { Text("Clear history?") },
        text = { Text("This only removes the list on this phone. Received files stay in Downloads/Ferry.") },
        confirmButton = { Button({ app.history.clear(); confirmClear = false }) { Text("Clear") } },
        dismissButton = { TextButton({ confirmClear = false }) { Text("Cancel") } },
    )
}

@Composable
fun TransferCard(t: Transfer, modifier: Modifier = Modifier) {
    val app = FerryApp.app
    val ex = LocalExtra.current
    Panel(modifier) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box {
                if (t.method == Method.LINK) IconBadge(Icons.Rounded.Link, Color(0xFF7A3DF0), 48.dp)
                else DeviceAvatar(t.peer, if (t.method == Method.SERVER) "desktop" else "mobile", 48.dp)
                IconBadge(if (t.sent) Icons.AutoMirrored.Rounded.CallMade else Icons.AutoMirrored.Rounded.CallReceived, MaterialTheme.colorScheme.primary, 20.dp,
                    filled = true)
            }
            Spacer(Modifier.width(14.dp))
            Column(Modifier.weight(1f)) {
                Text(if (t.method == Method.LINK) "Creating link" else (if (t.sent) "To " else "From ") + t.peer, style = MaterialTheme.typography.titleSmall,
                    maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text("${t.files.size} file${if (t.files.size == 1) "" else "s"} · ${formatBytes(t.total)}", style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            MethodChip(t.method)
        }
        Spacer(Modifier.height(16.dp))
        Row(verticalAlignment = Alignment.Bottom) {
            Text("${(t.progress * 100).toInt()}%", style = MaterialTheme.typography.headlineSmall)
            Spacer(Modifier.width(10.dp))
            Text(
                when {
                    t.status == TStatus.TRANSFERRING && t.speed > 0 -> "${formatSpeed(t.speed)} · ${formatEta((t.total - t.done) / t.speed)}"
                    t.note.isNotEmpty() -> t.note
                    else -> t.status.label
                },
                Modifier.weight(1f).padding(bottom = 3.dp), style = MaterialTheme.typography.bodySmall, maxLines = 2,
                color = if (t.status == TStatus.INTERRUPTED && !t.paused) ex.warn else MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Spacer(Modifier.height(10.dp))
        Progress(t.progress)
        Spacer(Modifier.height(12.dp))
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp, Alignment.End)) {
            if (t.canPause && t.status == TStatus.TRANSFERRING) FilledTonalIconButton({ app.transfers.pause(t.id) }) { Icon(Icons.Rounded.Pause, "Pause") }
            if (t.paused) FilledTonalIconButton({ app.transfers.resume(t.id) }) { Icon(Icons.Rounded.PlayArrow, "Resume") }
            FilledTonalIconButton({ app.transfers.cancel(t.id) }) { Icon(Icons.Rounded.Close, "Cancel") }
        }
    }
}

@Composable
private fun MethodChip(m: Method) {
    val ex = LocalExtra.current
    when (m) {
        Method.DIRECT -> Chip("Direct", ex.ok, ex.okSoft)
        Method.SERVER -> Chip("Server", MaterialTheme.colorScheme.primary, MaterialTheme.colorScheme.primaryContainer)
        Method.LINK -> Chip("Link", Color(0xFF7A3DF0), Color(0xFF7A3DF0).copy(alpha = 0.12f))
    }
}

@Composable
private fun HistoryRow(t: Transfer, onLink: (Transfer) -> Unit) {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val ex = LocalExtra.current
    val ok = t.status == TStatus.COMPLETED
    var menu by remember { mutableStateOf(false) }
    RowItem(
        title = if (t.method == Method.LINK) "Link · ${t.files.firstOrNull()?.name ?: ""}" else (if (t.sent) "To " else "From ") + t.peer,
        subtitle = if (!ok && t.error.isNotEmpty()) t.error else "${t.files.size} file${if (t.files.size == 1) "" else "s"} · ${formatBytes(t.total)} · ${relativeTime(t.updatedAt)}",
        leading = {
            Box {
                FileThumb(t.files.firstOrNull()?.uri, t.files.firstOrNull()?.name ?: "", t.files.firstOrNull()?.mime ?: "", 44.dp)
                Box(Modifier.align(Alignment.BottomEnd).padding(0.dp)) {
                    Icon(if (ok) Icons.Rounded.CheckCircle else Icons.Rounded.ErrorOutline, null, Modifier.size(18.dp),
                        tint = if (ok) ex.ok else ex.warn)
                }
            }
        },
        trailing = {
            Box {
                IconButton({ menu = true }) { Icon(Icons.Rounded.MoreVert, "Actions") }
                DropdownMenu(menu, { menu = false }) {
                    if (t.shareUrl.isNotEmpty()) {
                        DropdownMenuItem({ Text("Copy link") }, { menu = false; copyText(ctx, t.shareUrl) }, leadingIcon = { Icon(Icons.Rounded.ContentCopy, null) })
                        DropdownMenuItem({ Text("Share link") }, { menu = false; shareText(ctx, t.shareUrl) }, leadingIcon = { Icon(Icons.Rounded.Share, null) })
                    }
                    if (!ok && app.transfers.retryable.containsKey(t.id))
                        DropdownMenuItem({ Text("Retry") }, { menu = false; app.transfers.retryable[t.id]?.invoke() }, leadingIcon = { Icon(Icons.Rounded.Refresh, null) })
                    if (!ok && t.method == Method.DIRECT && t.sent && app.transfers.linkable.containsKey(t.id) && app.server.online)
                        DropdownMenuItem({ Text("Send as link instead") }, { menu = false; onLink(t) }, leadingIcon = { Icon(Icons.Rounded.Link, null) })
                    DropdownMenuItem({ Text("Remove from history") }, { menu = false; app.history.remove(t.id) }, leadingIcon = { Icon(Icons.Rounded.Delete, null) })
                }
            }
        },
    )
}
