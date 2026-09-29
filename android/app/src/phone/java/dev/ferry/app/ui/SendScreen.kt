package dev.ferry.app.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.BatteryAlert
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Computer
import androidx.compose.material.icons.rounded.Link
import androidx.compose.material.icons.rounded.QrCodeScanner
import androidx.compose.material.icons.rounded.Radar
import androidx.compose.material.icons.rounded.SignalCellularAlt
import androidx.compose.material.icons.rounded.Smartphone
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import dev.ferry.app.FerryApp
import dev.ferry.app.data.Peer
import dev.ferry.app.server.ServerState
import dev.ferry.app.transfer.Storage
import dev.ferry.app.util.PairCode
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.relativeTime
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private data class MyDevice(val id: String, val name: String, val platform: String, val online: Boolean, val lastSeen: Long)

/** A decision the user must make before a transfer starts (cellular data, low battery, method). */
private sealed class Gate {
    data class Cellular(val go: (wifiOnly: Boolean) -> Unit) : Gate()
    data class Battery(val go: () -> Unit) : Gate()
    data class ChooseMethod(val device: MyDevice, val peer: Peer) : Gate()
}

@Composable
fun SendScreen(nav: NavHostController) {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val files by SendQueue.files.collectAsState()
    val peers by app.discovery.peers.collectAsState()
    val scanning by app.discovery.scanning.collectAsState()
    val server by app.server.state.collectAsState()
    val net by app.net.state.collectAsState()
    val trustedList by app.prefs.trusted.collectAsState()
    var myDevices by remember { mutableStateOf<List<MyDevice>>(emptyList()) }
    var gate by remember { mutableStateOf<Gate?>(null) }
    var showLink by remember { mutableStateOf(false) }
    var showManual by remember { mutableStateOf(false) }
    val total = files.sumOf { it.size }
    val ex = LocalExtra.current

    DisposableEffect(Unit) {
        app.discovery.acquire("send")
        onDispose { app.discovery.release("send") }
    }
    LaunchedEffect(server) {
        val api = app.server.api() ?: return@LaunchedEffect
        if (server !is ServerState.Online) return@LaunchedEffect
        val list = withContext(Dispatchers.IO) { runCatching { api.get("/api/v1/devices").getJSONArray("devices") }.getOrNull() } ?: return@LaunchedEffect
        val objs = (0 until list.length()).map { list.getJSONObject(it) }
        myDevices = objs.filter { !it.optBoolean("current") }.map { MyDevice(it.getString("id"), it.getString("name"), it.optString("platform"), it.optBoolean("online"), it.optLong("lastSeen")) }
        app.discovery.addFromServer(objs)
    }
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        SendQueue.add(uris.mapNotNull { Storage.describe(ctx, it) })
    }

    fun started() {
        SendQueue.files.value = emptyList()
        nav.navigate("transfers") { popUpTo("home") }
    }
    // Large transfers over mobile data need confirmation; "Wi-Fi only" queues them.
    fun viaServer(go: (Boolean) -> Unit) {
        if (!net.wifi && net.cellular && total >= app.prefs.cellularWarnBytes) gate = Gate.Cellular(go) else go(false)
    }
    fun direct(go: () -> Unit) {
        if (total > 500L * 1024 * 1024 && app.net.batteryLow()) gate = Gate.Battery(go) else go()
    }
    fun requireFiles(action: () -> Unit) {
        if (files.isEmpty()) toast(ctx, "Add at least one file first") else action()
    }
    fun sendToPeer(p: Peer) = requireFiles { direct { app.transfers.sendDirect(p, files); started() } }
    fun sendToDevice(d: MyDevice) = requireFiles {
        val near = peers.values.find { it.accountDeviceId == d.id }
        when {
            app.prefs.method == "ask" && near != null -> gate = Gate.ChooseMethod(d, near)
            app.prefs.method == "server" || near == null -> {
                if (app.prefs.method == "direct") {
                    toast(ctx, "${d.name} isn't reachable on this network. Allow “Via server” in Settings → Transfer method.")
                    return@requireFiles
                }
                viaServer { wifiOnly -> app.transfers.sendViaServer(d.id, d.name, files, wifiOnly); started() }
            }
            else -> direct { app.transfers.sendDirect(near, files, fallbackDeviceId = d.id); started() }
        }
    }

    val nearby = peers.values.filter { it.accountDeviceId.isEmpty() }.sortedBy { it.alias.lowercase() }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader("Send", if (files.isEmpty()) "Choose what to send" else "${files.size} file${if (files.size == 1) "" else "s"} · ${formatBytes(total)}",
            onBack = { nav.popBackStack() })
        LazyColumn(Modifier.weight(1f), contentPadding = androidx.compose.foundation.layout.PaddingValues(bottom = 24.dp)) {
            // Selected files strip
            item {
                LazyRow(contentPadding = androidx.compose.foundation.layout.PaddingValues(horizontal = 20.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    item {
                        Column(
                            Modifier.width(96.dp).height(122.dp).clip(RoundedCornerShape(22.dp))
                                .border(1.5.dp, MaterialTheme.colorScheme.primary.copy(alpha = 0.4f), RoundedCornerShape(22.dp))
                                .background(MaterialTheme.colorScheme.primaryContainer.copy(alpha = 0.5f)).clickable { pick.pickFiles(ctx) },
                            horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center,
                        ) {
                            Icon(Icons.Rounded.Add, null, tint = MaterialTheme.colorScheme.primary, modifier = Modifier.size(28.dp))
                            Text("Add files", style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary)
                        }
                    }
                    items(files, key = { it.id }) { f ->
                        Box(Modifier.width(96.dp)) {
                            Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
                                FileThumb(f.uri, f.name, f.mime, 96.dp)
                                Text(f.name, style = MaterialTheme.typography.labelMedium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(top = 6.dp))
                                Text(formatBytes(f.size), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                            Box(Modifier.align(Alignment.TopEnd).padding(4.dp).size(26.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.55f))
                                .clickable { SendQueue.files.value = files - f }, contentAlignment = Alignment.Center) {
                                Icon(Icons.Rounded.Close, "Remove ${f.name}", tint = Color.White, modifier = Modifier.size(16.dp))
                            }
                        }
                    }
                }
            }

            // Nearby
            item {
                Column(Modifier.padding(horizontal = 20.dp)) {
                    SectionTitle("Nearby", action = {
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            PillAction(if (scanning) "Scanning…" else "Scan", Icons.Rounded.Radar) { if (!scanning && net.lan) app.discovery.scanSubnet() }
                            PillAction("Connect", Icons.Rounded.QrCodeScanner) { showManual = true }
                        }
                    })
                    when {
                        !net.lan -> Hint("Join a Wi-Fi network (or hotspot) shared with the other device to send directly — no internet needed.", Icons.Rounded.WifiOff, ex.warn)
                        nearby.isEmpty() -> Panel(padding = 20.dp) {
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                RadarPulse(true, size = 64.dp) { Icon(Icons.Rounded.Radar, null, tint = MaterialTheme.colorScheme.primary) }
                                Spacer(Modifier.width(14.dp))
                                Column(Modifier.weight(1f)) {
                                    Text("Looking for devices…", style = MaterialTheme.typography.titleSmall)
                                    Text("Open Ferry or LocalSend in receive mode on the other device. Not showing up? Tap Connect to scan its QR code.",
                                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                }
                            }
                        }
                        else -> nearby.chunked(3).forEach { row ->
                            Row(Modifier.fillMaxWidth().padding(bottom = 12.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                                row.forEach { p ->
                                    val trusted = trustedList.any { it.fingerprint.equals(p.fingerprint, true) }
                                    PeerTile(p, trusted, Modifier.weight(1f)) { sendToPeer(p) }
                                }
                                repeat(3 - row.size) { Spacer(Modifier.weight(1f)) }
                            }
                        }
                    }
                }
            }

            // My devices
            if (app.server.profile != null) item {
                Column(Modifier.padding(horizontal = 20.dp)) {
                    SectionTitle("My devices")
                    when {
                        server is ServerState.SignedOut -> Hint("Sign in to your server to see your devices.", Icons.Rounded.Smartphone)
                        server is ServerState.Offline -> Hint("Server unavailable — ${(server as ServerState.Offline).reason}", Icons.Rounded.Smartphone, ex.warn)
                        server !is ServerState.Online -> Hint("Connecting to your server…", Icons.Rounded.Smartphone)
                        myDevices.isEmpty() -> Hint("No other devices on your account yet. Sign in with Ferry on another phone to see it here.", Icons.Rounded.Smartphone)
                        else -> GroupCard(rows = myDevices.map { d ->
                            {
                                val near = peers.values.any { it.accountDeviceId == d.id }
                                RowItem(
                                    d.name,
                                    if (near) "On this network · direct" else if (d.online) "Via your server" else "Via your server · seen ${relativeTime(d.lastSeen, System.currentTimeMillis() + app.server.skewMs)}",
                                    leading = { DeviceAvatar(d.id, if (d.platform == "android") "mobile" else "desktop", 42.dp) },
                                    onClick = { sendToDevice(d) },
                                    trailing = { if (near) Chip("Direct", ex.ok, ex.okSoft) else Chip("Server") },
                                )
                            }
                        })
                    }
                }
            }

            // Link
            if (app.server.profile != null) item {
                Column(Modifier.padding(horizontal = 20.dp)) {
                    SectionTitle("Anyone")
                    val online = server is ServerState.Online
                    Panel(onClick = { if (online) requireFiles { showLink = true } else toast(ctx, "Your server must be online to create links.") }) {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            IconBadge(Icons.Rounded.Link, Color(0xFF7A3DF0), 48.dp)
                            Spacer(Modifier.width(14.dp))
                            Column(Modifier.weight(1f)) {
                                Text("Create a link", style = MaterialTheme.typography.titleSmall)
                                Text(if (online) "Recipients only need a browser — set expiry, password, one-time" else "Needs your server to be online",
                                    style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                    }
                }
            }
        }
    }

    when (val g = gate) {
        is Gate.Cellular -> ChoiceDialog(Icons.Rounded.SignalCellularAlt, "Use mobile data?",
            "You're not on Wi-Fi. Sending ${formatBytes(total)} over mobile data may cost money.",
            listOf("Continue" to { gate = null; g.go(false) }, "Wait for Wi-Fi" to { gate = null; g.go(true) }), onDismiss = { gate = null })
        is Gate.Battery -> ChoiceDialog(Icons.Rounded.BatteryAlert, "Battery is low",
            "This is a large transfer (${formatBytes(total)}). Plug in the charger to avoid interruption.",
            listOf("Send anyway" to { gate = null; g.go() }), onDismiss = { gate = null })
        is Gate.ChooseMethod -> ChoiceDialog(Icons.Rounded.Computer, "How should it travel?",
            "${g.device.name} is on this network. Direct is fastest and private; via server works even if the device goes offline.",
            listOf(
                "Direct" to { gate = null; direct { app.transfers.sendDirect(g.peer, files, g.device.id); started() } },
                "Via server" to { gate = null; viaServer { w -> app.transfers.sendViaServer(g.device.id, g.device.name, files, w); started() } },
            ), onDismiss = { gate = null })
        null -> {}
    }
    if (showLink) LinkOptionsDialog(onDismiss = { showLink = false }) { exp, max, pw ->
        showLink = false
        viaServer { w -> app.transfers.shareAsLink(files, exp, max, pw, w); started() }
    }
    if (showManual) ManualConnectDialog(onDismiss = { showManual = false }, onScan = { showManual = false; nav.navigate("scan") }) { input ->
        scope.launch {
            try {
                val p = connectInput(input)
                showManual = false
                toast(ctx, "Found ${p.alias}")
            } catch (e: Exception) {
                toast(ctx, e.message ?: "Couldn't connect")
            }
        }
    }
}

@Composable
private fun PeerTile(p: Peer, trusted: Boolean, modifier: Modifier, onClick: () -> Unit) {
    val ex = LocalExtra.current
    Column(
        modifier.clip(RoundedCornerShape(22.dp)).background(MaterialTheme.colorScheme.surface)
            .border(1.dp, if (trusted) ex.ok.copy(alpha = 0.5f) else MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f), RoundedCornerShape(22.dp))
            .clickable(onClick = onClick).padding(vertical = 16.dp, horizontal = 8.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        DeviceAvatar(p.fingerprint.ifEmpty { p.key }, p.type, 56.dp)
        Spacer(Modifier.height(10.dp))
        Text(p.alias, style = MaterialTheme.typography.labelLarge, maxLines = 1, overflow = TextOverflow.Ellipsis, textAlign = TextAlign.Center)
        Text(if (trusted) "Trusted" else if (p.ferry) "Ferry" else "LocalSend", style = MaterialTheme.typography.labelSmall,
            color = if (trusted) ex.ok else MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1)
    }
}

/** Compact decision dialog with an icon, text and one or more actions. */
@Composable
fun ChoiceDialog(icon: androidx.compose.ui.graphics.vector.ImageVector, title: String, text: String, actions: List<Pair<String, () -> Unit>>, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { IconBadge(icon, size = 52.dp) },
        title = { Text(title, textAlign = TextAlign.Center) },
        text = { Text(text, textAlign = TextAlign.Center) },
        confirmButton = {
            Column(Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                actions.forEachIndexed { i, (label, fn) -> BigButton(label, modifier = Modifier.fillMaxWidth(), tonal = i > 0, onClick = fn) }
                TextButton(onDismiss, Modifier.fillMaxWidth()) { Text("Cancel") }
            }
        },
    )
}

/** Link options as a bottom sheet: expiry, one-time, password. */
@OptIn(ExperimentalMaterial3Api::class, androidx.compose.foundation.layout.ExperimentalLayoutApi::class)
@Composable
fun LinkOptionsDialog(onDismiss: () -> Unit, onCreate: (expiresIn: Long, maxDownloads: Int, password: String?) -> Unit) {
    val expiries = listOf("1 hour" to 3600L, "1 day" to 86400L, "7 days" to 7 * 86400L, "30 days" to 30 * 86400L, "Never" to 0L)
    var exp by remember { mutableStateOf(7 * 86400L) }
    var once by remember { mutableStateOf(false) }
    var pw by remember { mutableStateOf("") }
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = MaterialTheme.colorScheme.surface) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 24.dp).padding(bottom = 20.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                IconBadge(Icons.Rounded.Link, Color(0xFF7A3DF0), 48.dp)
                Spacer(Modifier.width(14.dp))
                Column {
                    Text("Create a link", style = MaterialTheme.typography.titleLarge)
                    Text("Anyone with the link can download — no app needed", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            Text("Expires", style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(top = 22.dp, bottom = 8.dp))
            androidx.compose.foundation.layout.FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                expiries.forEach { (l, v) -> FilterChip(exp == v, { exp = v }, { Text(l) }, shape = RoundedCornerShape(50)) }
            }
            Row(Modifier.fillMaxWidth().padding(top = 14.dp).clip(RoundedCornerShape(18.dp)).background(MaterialTheme.colorScheme.surfaceVariant)
                .clickable { once = !once }.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
                Checkbox(once, { once = it })
                Column {
                    Text("One-time download", style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Medium)
                    Text("Works for exactly one recipient", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            OutlinedTextField(pw, { pw = it.take(72) }, Modifier.fillMaxWidth().padding(top = 14.dp), label = { Text("Password (optional)") }, singleLine = true,
                shape = RoundedCornerShape(16.dp), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password),
                visualTransformation = PasswordVisualTransformation(),
                supportingText = { if (pw.isNotEmpty() && pw.length < 4) Text("At least 4 characters") })
            Spacer(Modifier.height(12.dp))
            BigButton("Upload & create link", Icons.Rounded.Link, Modifier.fillMaxWidth(), enabled = pw.isEmpty() || pw.length >= 4) {
                onCreate(exp, if (once) 1 else 0, pw.ifEmpty { null })
            }
        }
    }
}

@Composable
private fun ManualConnectDialog(onDismiss: () -> Unit, onScan: () -> Unit, onConnect: (String) -> Unit) {
    var text by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { IconBadge(Icons.Rounded.QrCodeScanner, size = 52.dp) },
        title = { Text("Connect to a device") },
        text = {
            Column {
                Text("On the other device open Receive to see its QR code and pairing code.", style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Center)
                Spacer(Modifier.height(16.dp))
                BigButton("Scan QR code", Icons.Rounded.QrCodeScanner, Modifier.fillMaxWidth(), tonal = true, onClick = onScan)
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(text, { text = it }, Modifier.fillMaxWidth(), label = { Text("Pairing code or IP address") }, singleLine = true,
                    shape = RoundedCornerShape(16.dp))
            }
        },
        confirmButton = { TextButton({ onConnect(text) }, enabled = text.isNotBlank()) { Text("Connect") } },
        dismissButton = { TextButton(onDismiss) { Text("Cancel") } },
    )
}
