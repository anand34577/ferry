package dev.ferry.app.tv

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
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
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.DeleteSweep
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Link
import androidx.compose.material.icons.rounded.OpenInNew
import androidx.compose.material.icons.rounded.Pause
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.QrCode2
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.SwapVert
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Icon
import androidx.tv.material3.ListItem
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import dev.ferry.app.FerryApp
import dev.ferry.app.data.Method
import dev.ferry.app.data.TStatus
import dev.ferry.app.data.Transfer
import dev.ferry.app.transfer.Storage
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.formatEta
import dev.ferry.app.util.formatSpeed
import dev.ferry.app.util.relativeTime

@Composable
fun TransfersScreen(onViewFile: (LocalFile) -> Unit) {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val active by app.transfers.active.collectAsState()
    val history by app.history.items.collectAsState()
    var details by remember { mutableStateOf<Transfer?>(null) }
    var linkQr by remember { mutableStateOf<Transfer?>(null) }
    var confirmClear by remember { mutableStateOf(false) }

    Page("Transfers", "${active.size} in progress · ${history.size} in history", actions = {
        if (history.isNotEmpty()) Action("Clear history", Icons.Rounded.DeleteSweep, primary = false) { confirmClear = true }
    }) {
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 40.dp, start = 4.dp, end = 4.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            if (active.isEmpty() && history.isEmpty()) item {
                Empty(Icons.Rounded.SwapVert, "No transfers yet", "Files you send or receive show their progress here.")
            }
            if (active.isNotEmpty()) item { SectionTitle("In progress") }
            items(active, key = { "a" + it.id }) { t -> ActiveCard(t) }
            if (history.isNotEmpty()) item { SectionTitle("History", Modifier.padding(top = 10.dp)) }
            items(history, key = { "h" + it.id }) { t ->
                val ok = t.status == TStatus.COMPLETED
                ListItem(
                    selected = false,
                    onClick = { if (t.shareUrl.isNotEmpty()) linkQr = t else details = t },
                    headlineContent = {
                        Text(if (t.method == Method.LINK) "Link · ${t.files.firstOrNull()?.name ?: ""}" else (if (t.sent) "To " else "From ") + t.peer,
                            maxLines = 1, overflow = TextOverflow.Ellipsis)
                    },
                    supportingContent = {
                        Text(if (!ok && t.error.isNotEmpty()) t.error else "${t.files.size} file${if (t.files.size == 1) "" else "s"} · ${formatBytes(t.total)} · ${relativeTime(t.updatedAt)}",
                            maxLines = 1, overflow = TextOverflow.Ellipsis)
                    },
                    leadingContent = {
                        Icon(if (t.sent) Icons.AutoMirrored.Rounded.CallMade else Icons.AutoMirrored.Rounded.CallReceived, null, Modifier.size(22.dp))
                    },
                    trailingContent = {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            if (t.shareUrl.isNotEmpty()) Pill("QR", Tv.violet, Icons.Rounded.QrCode2)
                            Spacer(Modifier.width(10.dp))
                            Icon(if (ok) Icons.Rounded.CheckCircle else Icons.Rounded.ErrorOutline, t.status.label, tint = if (ok) Tv.ok else Tv.warn)
                        }
                    },
                )
            }
        }
    }

    details?.let { t ->
        val ok = t.status == TStatus.COMPLETED
        val first = t.files.firstOrNull()
        val canOpen = ok && !t.sent && first?.uri != null
        val canRetry = !ok && app.transfers.retryable.containsKey(t.id)
        TvDialog({ details = null }) {
            Text((if (t.sent) "To " else "From ") + t.peer, style = MaterialTheme.typography.titleLarge)
            Text("${t.status.label} · ${t.method.label} · ${relativeTime(t.updatedAt)}", style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
            if (t.error.isNotEmpty()) Text(t.error, Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodyMedium, color = Tv.warn)
            Column(Modifier.padding(top = 12.dp)) {
                t.files.take(6).forEach { f -> Text("• ${f.name} · ${formatBytes(f.size)}", style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis) }
                if (t.files.size > 6) Text("and ${t.files.size - 6} more", style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
            }
            Row(Modifier.fillMaxWidth().padding(top = 20.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                val focus = Modifier.focusRequester(rememberInitialFocus())
                if (canOpen) Action("Open", Icons.Rounded.OpenInNew, focus) {
                    details = null
                    openFile(ctx, LocalFile(first!!.uri!!, first.name, first.size, first.mime.ifEmpty { Storage.mimeFor(first.name) }, t.updatedAt), onViewFile)
                }
                if (canRetry) Action("Retry", Icons.Rounded.Refresh, if (canOpen) Modifier else focus) { details = null; app.transfers.retryable[t.id]?.invoke() }
                Action("Remove from history", Icons.Rounded.Delete, if (canOpen || canRetry) Modifier else focus, primary = false) {
                    details = null; app.history.remove(t.id)
                }
            }
        }
    }
    linkQr?.let { t ->
        TvDialog({ linkQr = null }, width = 620.dp) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                QrCode(t.shareUrl, 170.dp)
                Column(Modifier.padding(start = 20.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(Icons.Rounded.Link, null, tint = Tv.violet)
                        Spacer(Modifier.width(8.dp))
                        Text("Download link", style = MaterialTheme.typography.titleLarge)
                    }
                    Text("Scan with a phone camera. Anyone with the link can download ${t.files.size} file${if (t.files.size == 1) "" else "s"}.",
                        Modifier.padding(top = 6.dp), style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
                    Text(t.shareUrl, Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodySmall, maxLines = 3)
                    Row(Modifier.padding(top = 16.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                        Action("Done", modifier = Modifier.focusRequester(rememberInitialFocus())) { linkQr = null }
                        Action("Remove", Icons.Rounded.Delete, primary = false) { linkQr = null; app.history.remove(t.id) }
                    }
                }
            }
        }
    }
    if (confirmClear) ConfirmDialog("Clear history?", "Only the list is cleared. Received files stay in Files.", "Clear", { confirmClear = false }) {
        confirmClear = false; app.history.clear()
    }
}

@Composable
private fun ActiveCard(t: Transfer) {
    val app = FerryApp.app
    Panel(Modifier.fillMaxWidth()) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Avatar(t.peer, if (t.method == Method.SERVER) "desktop" else "mobile", 40.dp)
            Column(Modifier.weight(1f).padding(horizontal = 12.dp)) {
                Text(if (t.method == Method.LINK) "Creating a link" else (if (t.sent) "Sending to " else "Receiving from ") + t.peer,
                    style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text("${t.files.size} file${if (t.files.size == 1) "" else "s"} · ${formatBytes(t.total)} · ${t.method.label}", style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
            }
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (t.canPause && t.status == TStatus.TRANSFERRING) Action("Pause", Icons.Rounded.Pause, primary = false) { app.transfers.pause(t.id) }
                if (t.paused) Action("Resume", Icons.Rounded.PlayArrow) { app.transfers.resume(t.id) }
                Action("Cancel", Icons.Rounded.Close, primary = false) { app.transfers.cancel(t.id) }
            }
        }
        Bar(t.progress, Modifier.padding(top = 12.dp, bottom = 6.dp))
        Text(
            "${(t.progress * 100).toInt()}%  " + when {
                t.status == TStatus.TRANSFERRING && t.speed > 0 -> "${formatSpeed(t.speed)} · ${formatEta((t.total - t.done) / t.speed)}"
                t.note.isNotEmpty() -> t.note
                else -> t.status.label
            },
            style = MaterialTheme.typography.bodyMedium, color = if (t.status == TStatus.INTERRUPTED && !t.paused) Tv.warn else Tv.muted,
        )
    }
}
