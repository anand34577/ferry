package dev.ferry.app.tv

import android.net.Uri
import androidx.compose.animation.core.RepeatMode
import androidx.compose.animation.core.animateFloat
import androidx.compose.animation.core.infiniteRepeatable
import androidx.compose.animation.core.rememberInfiniteTransition
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Lock
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Stop
import androidx.compose.material.icons.rounded.Wifi
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import dev.ferry.app.FerryApp
import dev.ferry.app.data.TStatus
import dev.ferry.app.transfer.Storage
import dev.ferry.app.util.PairCode
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.formatSpeed
import dev.ferry.app.util.relativeTime

@Composable
fun ReceiveScreen(onViewFile: (LocalFile) -> Unit) {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val receiving by app.receivingFlow.collectAsState()
    val net by app.net.state.collectAsState()
    val running by app.discovery.running.collectAsState()
    val active by app.transfers.active.collectAsState()
    val history by app.history.items.collectAsState()
    val incoming = active.filter { !it.sent }
    val recent = history.filter { !it.sent && it.status == TStatus.COMPLETED }.flatMap { t -> t.files.mapNotNull { f -> f.uri?.let { t to f } } }.take(8)
    val visible = receiving && net.lan && running

    // Keep the TV awake while it's showing the code, so the screensaver doesn't hide it.
    val view = LocalView.current
    DisposableEffect(visible) {
        view.keepScreenOn = visible
        onDispose { view.keepScreenOn = false }
    }

    Row(Modifier.fillMaxSize().padding(start = 24.dp, end = 48.dp, top = 24.dp, bottom = 24.dp), horizontalArrangement = Arrangement.spacedBy(28.dp)) {
        Column(Modifier.weight(1f).fillMaxHeight().verticalScroll(rememberScrollState())) {
            Text("Receive", style = MaterialTheme.typography.headlineMedium, fontWeight = FontWeight.Bold)
            Spacer(Modifier.height(18.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Pulse(visible, 84.dp) { Avatar(app.certs.fingerprint, "tv", 56.dp) }
                Spacer(Modifier.width(16.dp))
                Column(Modifier.weight(1f)) {
                    Text(app.prefs.alias, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(
                        when {
                            !receiving -> "Receiving is off. Nearby devices can't see this TV."
                            !net.lan -> "Waiting for a Wi-Fi or Ethernet network…"
                            else -> "Ready. You approve every transfer."
                        },
                        style = MaterialTheme.typography.bodyLarge, color = if (visible) Tv.ok else Tv.muted,
                    )
                    Row(Modifier.padding(top = 8.dp), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        if (net.lan) Pill("On the network", Tv.ok, Icons.Rounded.Wifi) else Pill("No network", Tv.warn, Icons.Rounded.WifiOff)
                        if (app.prefs.requirePin && app.prefs.pin.isNotEmpty()) Pill("PIN ${app.prefs.pin}", Tv.violet, Icons.Rounded.Lock)
                    }
                }
            }
            Spacer(Modifier.height(18.dp))
            Action(if (receiving) "Stop receiving" else "Start receiving", if (receiving) Icons.Rounded.Stop else Icons.Rounded.PlayArrow,
                Modifier.focusRequester(rememberInitialFocus()), primary = !receiving) { app.setReceiving(!receiving) }

            incoming.firstOrNull()?.let { t ->
                SectionTitle("Receiving now", Modifier.padding(top = 18.dp))
                Panel(Modifier.fillMaxWidth()) {
                    Text("From ${t.peer} · ${t.files.size} file${if (t.files.size == 1) "" else "s"} · ${formatBytes(t.total)}", style = MaterialTheme.typography.titleMedium)
                    Bar(t.progress, Modifier.padding(vertical = 10.dp))
                    Text("${(t.progress * 100).toInt()}%  " + (if (t.status == TStatus.TRANSFERRING && t.speed > 0) formatSpeed(t.speed) else t.note.ifEmpty { t.status.label }),
                        style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
                }
            }

            if (recent.isEmpty() && incoming.isEmpty()) {
                SectionTitle("How to send to this TV", Modifier.padding(top = 22.dp))
                listOf(
                    "Open Ferry or LocalSend on your phone or computer (same Wi-Fi).",
                    "Choose files, then pick this TV — or scan the QR code.",
                    "Accept here with OK. Files appear under Files.",
                ).forEachIndexed { i, step ->
                    Row(Modifier.padding(vertical = 5.dp), verticalAlignment = Alignment.CenterVertically) {
                        Text("${i + 1}", Modifier.width(28.dp), style = MaterialTheme.typography.titleLarge, color = Tv.primary, fontWeight = FontWeight.Bold)
                        Text(step, style = MaterialTheme.typography.bodyLarge, color = Tv.muted)
                    }
                }
            }

            if (recent.isNotEmpty()) {
                SectionTitle("Just received", Modifier.padding(top = 18.dp))
                LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp), contentPadding = androidx.compose.foundation.layout.PaddingValues(vertical = 6.dp, horizontal = 4.dp)) {
                    items(recent, key = { it.second.uri.toString() }) { (t, f) ->
                        val lf = LocalFile(f.uri!!, f.name, f.size, f.mime.ifEmpty { Storage.mimeFor(f.name) }, t.updatedAt)
                        FocusCard({ openFile(ctx, lf, onViewFile) }, Modifier.width(150.dp)) {
                            Column {
                                Thumb(lf.uri, lf.name, lf.mime, Modifier.fillMaxWidth().height(84.dp))
                                Column(Modifier.padding(8.dp)) {
                                    Text(f.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    Text("${t.peer} · ${relativeTime(t.updatedAt)}", style = MaterialTheme.typography.bodySmall, color = Tv.muted, maxLines = 1)
                                }
                            }
                        }
                    }
                }
            }
        }

        // Right: how to reach this TV.
        Panel(Modifier.width(330.dp).fillMaxHeight(), padding = 18.dp) {
            if (visible) {
                val ip = net.lanAddrs.firstOrNull() ?: ""
                val port = app.discovery.port
                val qr = "ferry://peer?h=" + Uri.encode(net.lanAddrs.joinToString(",")) + "&p=$port&f=" + app.certs.fingerprint + "&n=" + Uri.encode(app.prefs.alias) + "&s=https"
                Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                    Text("Send to this TV", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                    Text("Scan with Ferry on your phone", style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
                    Spacer(Modifier.height(12.dp))
                    QrCode(qr, 170.dp)
                    Spacer(Modifier.height(12.dp))
                    Text("or enter this code in Ferry → Send → Connect", style = MaterialTheme.typography.bodySmall, color = Tv.muted)
                    Spacer(Modifier.height(6.dp))
                    CodeBoxes(PairCode.encode(ip, port) ?: ip)
                    Spacer(Modifier.height(8.dp))
                    Text("LocalSend: pick “${app.prefs.alias}” from the device list · IP $ip", style = MaterialTheme.typography.bodySmall,
                        color = Tv.muted, textAlign = TextAlign.Center)
                }
            } else Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                Empty(if (receiving) Icons.Rounded.WifiOff else Icons.Rounded.PlayArrow,
                    if (receiving) "Not on a network" else "Receiving is off",
                    if (receiving) "Connect this TV to the same Wi-Fi as your phone or computer. No internet needed."
                    else "Turn on receiving to show the code phones and computers use to send files here.")
            }
        }
    }
}

/** Soft rings radiating from the avatar while the TV is visible to nearby devices. */
@Composable
private fun Pulse(active: Boolean, size: Dp, content: @Composable () -> Unit) {
    val t = rememberInfiniteTransition(label = "pulse")
    val p by t.animateFloat(0f, 1f, infiniteRepeatable(tween(2200), RepeatMode.Restart), label = "p")
    Box(Modifier.size(size), contentAlignment = Alignment.Center) {
        if (active) Canvas(Modifier.fillMaxSize()) {
            for (k in 0..1) {
                val f = (p + k * 0.5f) % 1f
                drawCircle(Tv.ok.copy(alpha = (1f - f) * 0.5f), radius = this.size.minDimension / 2 * (0.66f + 0.34f * f), style = Stroke(3.dp.toPx()))
            }
        }
        Box(Modifier.clip(RoundedCornerShape(50))) { content() }
    }
}
