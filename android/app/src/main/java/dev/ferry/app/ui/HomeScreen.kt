package dev.ferry.app.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.CallMade
import androidx.compose.material.icons.automirrored.rounded.CallReceived
import androidx.compose.material.icons.automirrored.rounded.Send
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.Language
import androidx.compose.material.icons.rounded.Link
import androidx.compose.material.icons.rounded.QrCodeScanner
import androidx.compose.material.icons.rounded.Sensors
import androidx.compose.material.icons.rounded.Wifi
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import dev.ferry.app.FerryApp
import dev.ferry.app.data.TStatus
import dev.ferry.app.server.ServerState
import dev.ferry.app.transfer.Storage
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.relativeTime

@Composable
fun HomeScreen(nav: NavHostController) {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val net by app.net.state.collectAsState()
    val server by app.server.state.collectAsState()
    val receiving by app.receivingFlow.collectAsState()
    val active by app.transfers.active.collectAsState()
    val history by app.history.items.collectAsState()
    val ex = LocalExtra.current
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        if (uris.isNotEmpty()) {
            SendQueue.add(uris.mapNotNull { Storage.describe(ctx, it) })
            nav.navigate("send")
        }
    }

    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp)) {
        // Header
        Row(Modifier.padding(top = 12.dp, bottom = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            BrandMark(38.dp)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text("Ferry", style = MaterialTheme.typography.titleLarge)
                Text(app.prefs.alias, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
            }
            Box(Modifier.size(44.dp).clip(CircleShape).background(MaterialTheme.colorScheme.surface).clickable { nav.navigate("scan") },
                contentAlignment = Alignment.Center) { Icon(Icons.Rounded.QrCodeScanner, "Scan QR code") }
        }
        Text("Share anything,\nanywhere.", style = MaterialTheme.typography.displaySmall, modifier = Modifier.padding(top = 18.dp, bottom = 14.dp))

        // Connectivity: Wi-Fi, internet and server are independent.
        Row(Modifier.horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            if (net.lan) Chip(if (net.wifi) "Wi-Fi" else "Local network", ex.ok, ex.okSoft, Icons.Rounded.Wifi) else Chip("No Wi-Fi", icon = Icons.Rounded.WifiOff)
            if (net.internet) Chip("Internet", ex.ok, ex.okSoft, Icons.Rounded.Language) else Chip("Offline", icon = Icons.Rounded.Language)
            when (server) {
                is ServerState.Online -> Chip(app.server.profile?.name ?: "Server", ex.ok, ex.okSoft, Icons.Rounded.Cloud)
                is ServerState.None -> {}
                is ServerState.Checking -> Chip("Server…", icon = Icons.Rounded.Cloud)
                is ServerState.SignedOut -> Chip("Sign in needed", ex.warn, ex.warnSoft, Icons.Rounded.Cloud)
                else -> Chip("Server offline", ex.warn, ex.warnSoft, Icons.Rounded.CloudOff)
            }
        }
        val hint = when {
            net.lan && !net.internet -> "No internet — nearby devices on this network can still send and receive directly."
            !net.lan && net.internet -> "Not on Wi-Fi. Links and your server still work; nearby transfer needs a shared network or hotspot."
            !net.lan && !net.internet -> "You're offline. Join a Wi-Fi network or turn on your hotspot to share with people nearby."
            server is ServerState.Offline -> "Your server can't be reached right now. Nearby transfers still work."
            else -> null
        }
        if (hint != null) {
            Spacer(Modifier.height(12.dp)); Hint(hint, Icons.Rounded.Sensors)
        }

        // Hero: Send
        Spacer(Modifier.height(18.dp))
        Box(
            Modifier.fillMaxWidth().clip(RoundedCornerShape(30.dp)).background(BrandGradient).clickable { pick.launch(arrayOf("*/*")) }.padding(22.dp),
        ) {
            // soft decorative circles
            Box(Modifier.align(Alignment.TopEnd).offset(x = 60.dp, y = (-70).dp).size(170.dp).clip(CircleShape).background(Color.White.copy(alpha = 0.10f)))
            Box(Modifier.align(Alignment.BottomEnd).offset(x = 30.dp, y = 40.dp).size(110.dp).clip(CircleShape).background(Color.White.copy(alpha = 0.07f)))
            Column {
                Box(Modifier.size(52.dp).clip(RoundedCornerShape(18.dp)).background(Color.White.copy(alpha = 0.2f)), contentAlignment = Alignment.Center) {
                    Icon(Icons.AutoMirrored.Rounded.Send, null, tint = Color.White, modifier = Modifier.size(26.dp))
                }
                Spacer(Modifier.height(18.dp))
                Text("Send files", style = MaterialTheme.typography.headlineSmall, color = Color.White)
                Text("To a nearby device, your devices, or anyone with a link", style = MaterialTheme.typography.bodyMedium, color = Color.White.copy(alpha = 0.85f))
                Spacer(Modifier.height(18.dp))
                Row(Modifier.clip(RoundedCornerShape(50)).background(Color.White).padding(horizontal = 18.dp, vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Rounded.Add, null, tint = Color(0xFF2456F5), modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text("Choose files", color = Color(0xFF2456F5), style = MaterialTheme.typography.labelLarge)
                }
            }
        }

        // Tiles: Receive + Upload link
        Spacer(Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            Tile(Modifier.weight(1f), if (receiving) "Receiving" else "Receive", if (receiving) "Visible nearby" else "From nearby devices",
                Icons.AutoMirrored.Rounded.CallReceived, if (receiving) ex.ok else MaterialTheme.colorScheme.primary, live = receiving) { nav.navigate("receive") }
            Tile(Modifier.weight(1f), "Scan", "QR or pairing code", Icons.Rounded.QrCodeScanner, Color(0xFF7A3DF0)) { nav.navigate("scan") }
        }

        // In progress
        val running = active.filter { it.status.active }
        if (running.isNotEmpty()) {
            SectionTitle("In progress", action = { TextButton({ nav.navigate("transfers") }) { Text("See all") } })
            running.take(3).forEach { t -> TransferCard(t); Spacer(Modifier.height(10.dp)) }
        }

        // Recent
        if (history.isNotEmpty()) {
            SectionTitle("Recent", action = { TextButton({ nav.navigate("transfers") }) { Text("History") } })
            GroupCard(rows = history.take(3).map { t ->
                {
                    RowItem(
                        title = if (t.method == dev.ferry.app.data.Method.LINK) "Link · ${t.files.firstOrNull()?.name ?: ""}" else (if (t.sent) "To " else "From ") + t.peer,
                        subtitle = "${t.files.size} file${if (t.files.size == 1) "" else "s"} · ${formatBytes(t.total)} · ${relativeTime(t.updatedAt)}",
                        icon = when {
                            t.method == dev.ferry.app.data.Method.LINK -> Icons.Rounded.Link
                            t.sent -> Icons.AutoMirrored.Rounded.CallMade
                            else -> Icons.AutoMirrored.Rounded.CallReceived
                        },
                        tint = if (t.status == TStatus.COMPLETED) MaterialTheme.colorScheme.primary else ex.warn,
                        onClick = { nav.navigate("transfers") },
                    )
                }
            })
        }

        // Server
        if (server is ServerState.None || server is ServerState.SignedOut) {
            SectionTitle("Your server")
            Panel(onClick = { nav.navigate(if (server is ServerState.None) "servers" else "server") }) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    IconBadge(Icons.Rounded.Cloud, size = 48.dp)
                    Spacer(Modifier.width(14.dp))
                    Column(Modifier.weight(1f)) {
                        Text(if (server is ServerState.None) "Connect your Ferry server" else "Sign in to ${app.server.profile?.name}", style = MaterialTheme.typography.titleSmall)
                        Text("Optional — create links for anyone and reach your devices from anywhere.", style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
        }
        Spacer(Modifier.height(NavBarSpace))
    }
}

@Composable
private fun Tile(modifier: Modifier, title: String, subtitle: String, icon: ImageVector, tint: Color, live: Boolean = false, onClick: () -> Unit) {
    val shape = RoundedCornerShape(26.dp)
    Column(
        modifier.clip(shape).background(MaterialTheme.colorScheme.surface).border(1.dp, MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f), shape)
            .clickable(onClick = onClick).padding(18.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            IconBadge(icon, tint, 44.dp)
            Spacer(Modifier.weight(1f))
            if (live) StatusDot(true)
        }
        Spacer(Modifier.height(16.dp))
        Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
        Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}
