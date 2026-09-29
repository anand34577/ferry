package dev.ferry.app.tv

import android.content.Context
import android.content.Intent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Dialpad
import androidx.compose.material.icons.rounded.FolderOpen
import androidx.compose.material.icons.rounded.Link
import androidx.compose.material.icons.rounded.Radar
import androidx.compose.material.icons.rounded.SdStorage
import androidx.compose.material.icons.rounded.WifiOff
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import dev.ferry.app.FerryApp
import dev.ferry.app.data.Peer
import dev.ferry.app.server.ServerState
import dev.ferry.app.transfer.Storage
import dev.ferry.app.ui.SendQueue
import dev.ferry.app.ui.connectInput
import dev.ferry.app.util.formatBytes
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private data class MyDevice(val id: String, val name: String, val platform: String, val online: Boolean)

/** True when the TV has a real document picker (many only ship a stub that shows "no app can do this"). */
private fun hasPicker(ctx: Context): Boolean {
    val pkg = Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*").resolveActivity(ctx.packageManager)?.packageName
    return pkg != null && pkg != "com.android.tv.frameworkpackagestubs"
}

@Composable
fun SendScreen(onPickFromFiles: () -> Unit, onStarted: () -> Unit) {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val files by SendQueue.files.collectAsState()
    val peers by app.discovery.peers.collectAsState()
    val scanning by app.discovery.scanning.collectAsState()
    val server by app.server.state.collectAsState()
    val net by app.net.state.collectAsState()
    val trusted by app.prefs.trusted.collectAsState()
    var myDevices by remember { mutableStateOf<List<MyDevice>>(emptyList()) }
    var enterCode by remember { mutableStateOf(false) }
    var connecting by remember { mutableStateOf(false) }
    var confirmLink by remember { mutableStateOf(false) }
    val picker = remember { hasPicker(ctx) }
    val total = files.sumOf { it.size }

    DisposableEffect(Unit) {
        app.discovery.acquire("send")
        onDispose { app.discovery.release("send") }
    }
    LaunchedEffect(server) {
        val api = app.server.api() ?: return@LaunchedEffect
        if (server !is ServerState.Online) return@LaunchedEffect
        val list = withContext(Dispatchers.IO) { runCatching { api.get("/api/v1/devices").getJSONArray("devices") }.getOrNull() } ?: return@LaunchedEffect
        val objs = (0 until list.length()).map { list.getJSONObject(it) }
        myDevices = objs.filter { !it.optBoolean("current") }.map { MyDevice(it.getString("id"), it.getString("name"), it.optString("platform"), it.optBoolean("online")) }
        app.discovery.addFromServer(objs)
    }
    val pick = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        SendQueue.add(uris.mapNotNull { Storage.describe(ctx, it) })
    }

    fun withFiles(action: () -> Unit) {
        if (files.isEmpty()) toast(ctx, "Choose files to send first") else action()
    }
    fun started() {
        SendQueue.files.value = emptyList()
        onStarted()
    }
    fun sendTo(p: Peer) = withFiles { app.transfers.sendDirect(p, files); started() }
    fun sendTo(d: MyDevice) = withFiles {
        val near = peers.values.find { it.accountDeviceId == d.id }
        when {
            near != null && app.prefs.method != "server" -> app.transfers.sendDirect(near, files, fallbackDeviceId = d.id)
            app.prefs.method == "direct" -> {
                toast(ctx, "${d.name} isn't on this network. Allow “Via server” in Settings → Transfer method.")
                return@withFiles
            }
            else -> app.transfers.sendViaServer(d.id, d.name, files)
        }
        started()
    }

    val nearby = peers.values.filter { it.accountDeviceId.isEmpty() }.sortedBy { it.alias.lowercase() }
    val online = server is ServerState.Online

    Page("Send", if (files.isEmpty()) "Choose what to send, then where" else "${files.size} file${if (files.size == 1) "" else "s"} · ${formatBytes(total)}") {
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 40.dp)) {
            item {
                SectionTitle("1 · Files")
                LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp), contentPadding = PaddingValues(horizontal = 4.dp, vertical = 8.dp)) {
                    item { Tile(Icons.Rounded.FolderOpen, "Add from Files", "Things sent to this TV", Tv.primary, onClick = onPickFromFiles) }
                    if (picker) item { Tile(Icons.Rounded.SdStorage, "Browse storage", "USB drives and other apps", Tv.violet) { pick.launch(arrayOf("*/*")) } }
                    items(files, key = { it.id }) { f ->
                        FocusCard({ SendQueue.files.value = files - f; toast(ctx, "Removed ${f.name}") }, Modifier.width(150.dp)) {
                            Column {
                                Thumb(f.uri, f.name, f.mime, Modifier.fillMaxWidth().height(76.dp))
                                Column(Modifier.padding(8.dp)) {
                                    Text(f.name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                    Text("${formatBytes(f.size)} · OK to remove", style = MaterialTheme.typography.bodySmall, color = Tv.muted)
                                }
                            }
                        }
                    }
                }
            }
            item {
                SectionTitle("2 · Nearby devices", Modifier.padding(top = 12.dp))
                if (!net.lan) Panel(Modifier.fillMaxWidth()) {
                    Text("This TV isn't on a network. Connect it to the same Wi-Fi as the other device.", style = MaterialTheme.typography.bodyLarge, color = Tv.warn)
                }
                else LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp), contentPadding = PaddingValues(horizontal = 4.dp, vertical = 8.dp)) {
                    items(nearby, key = { it.key }) { p ->
                        val isTrusted = trusted.any { it.fingerprint.equals(p.fingerprint, true) }
                        DeviceTile(p.alias, if (isTrusted) "Trusted" else if (p.ferry) "Ferry" else "LocalSend", p.fingerprint.ifEmpty { p.key }, p.type,
                            if (isTrusted) Tv.ok else Tv.muted) { sendTo(p) }
                    }
                    item { Tile(Icons.Rounded.Dialpad, "Enter a code", "Shown on the other device", Tv.primary) { enterCode = true } }
                    item {
                        Tile(Icons.Rounded.Radar, if (scanning) "Searching…" else "Search network", "Finds devices that don't announce themselves", Tv.violet) {
                            if (!scanning) app.discovery.scanSubnet()
                        }
                    }
                }
                if (net.lan && nearby.isEmpty()) Text("Open Ferry or LocalSend on the other device and turn on receiving. It appears here automatically.",
                    Modifier.padding(top = 4.dp), style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
            }
            if (app.server.profile != null) item {
                SectionTitle("3 · Your devices and links", Modifier.padding(top = 12.dp))
                if (!online) Text("Your server is ${if (server is ServerState.SignedOut) "signed out" else "not reachable"} — see Server.",
                    style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
                else LazyRow(horizontalArrangement = Arrangement.spacedBy(12.dp), contentPadding = PaddingValues(horizontal = 4.dp, vertical = 8.dp)) {
                    items(myDevices, key = { it.id }) { d ->
                        val near = peers.values.any { it.accountDeviceId == d.id }
                        DeviceTile(d.name, if (near) "Same network · direct" else if (d.online) "Via your server" else "Offline · via server", d.id,
                            if (d.platform == "android") "mobile" else "desktop", if (near) Tv.ok else Tv.muted) { sendTo(d) }
                    }
                    item { Tile(Icons.Rounded.Link, "Create a link", "Shown as a QR code to scan", Tv.violet) { withFiles { confirmLink = true } } }
                }
            }
        }
    }

    if (enterCode) InputDialog("Connect with a code", "Pairing code or IP address", hint = "Find it on the other device under Receive, e.g. 60A-R0BQ or 192.168.1.20.",
        confirm = "Connect", keyboard = KeyboardType.Ascii, onDismiss = { enterCode = false }) { input ->
        enterCode = false
        scope.launch {
            connecting = true
            try {
                val p = connectInput(input)
                toast(ctx, "Found ${p.alias}" + if (files.isEmpty()) " — now choose files to send" else "")
            } catch (e: Exception) {
                toast(ctx, e.message ?: "Couldn't connect")
            } finally {
                connecting = false
            }
        }
    }
    if (connecting) WorkingDialog("Connecting…")
    if (confirmLink) ConfirmDialog("Create a link?", "Uploads ${formatBytes(total)} to your server. Anyone with the link can download for 7 days. " +
        "The link appears as a QR code under Transfers, ready to scan with a phone.", "Create link", { confirmLink = false }) {
        confirmLink = false
        app.transfers.shareAsLink(files, 7 * 86400L, 0, null)
        started()
    }
}

@Composable
private fun Tile(icon: ImageVector, title: String, subtitle: String, tint: Color, onClick: () -> Unit) {
    FocusCard(onClick, Modifier.width(150.dp).height(116.dp)) {
        Column(Modifier.fillMaxSize().padding(10.dp), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
            Box(Modifier.size(36.dp), contentAlignment = Alignment.Center) { Icon(icon, null, Modifier.size(28.dp), tint = tint) }
            Spacer(Modifier.height(4.dp))
            Text(title, style = MaterialTheme.typography.titleSmall, textAlign = TextAlign.Center, maxLines = 1)
            Text(subtitle, style = MaterialTheme.typography.bodySmall, color = Tv.muted, textAlign = TextAlign.Center, maxLines = 2)
        }
    }
}

@Composable
private fun DeviceTile(name: String, subtitle: String, key: String, type: String, subColor: Color, onClick: () -> Unit) {
    FocusCard(onClick, Modifier.width(150.dp).height(116.dp)) {
        Column(Modifier.fillMaxSize().padding(10.dp), verticalArrangement = Arrangement.Center, horizontalAlignment = Alignment.CenterHorizontally) {
            Avatar(key, type, 44.dp)
            Spacer(Modifier.height(6.dp))
            Text(name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(subtitle, style = MaterialTheme.typography.bodySmall, color = subColor, maxLines = 1)
        }
    }
}
