package dev.ferry.app.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Badge
import androidx.compose.material.icons.rounded.CleaningServices
import androidx.compose.material.icons.rounded.Cloud
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.Notifications
import androidx.compose.material.icons.rounded.Palette
import androidx.compose.material.icons.rounded.Route
import androidx.compose.material.icons.rounded.SignalCellularAlt
import androidx.compose.material.icons.rounded.Storage
import androidx.compose.material.icons.rounded.Sync
import androidx.compose.material.icons.rounded.VerifiedUser
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import dev.ferry.app.BuildConfig
import dev.ferry.app.FerryApp
import dev.ferry.app.server.ServerState
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.relativeTime
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject

private data class Picker(val title: String, val options: List<Pair<String, String>>, val selected: String, val onPick: (String) -> Unit)

@Composable
fun SettingsScreen(nav: NavHostController) {
    val app = FerryApp.app
    val p = app.prefs
    val ctx = LocalContext.current
    val theme by p.themeFlow.collectAsState()
    val server by app.server.state.collectAsState()
    var method by remember { mutableStateOf(p.method) }
    var notify by remember { mutableStateOf(p.notifyLevel) }
    var cell by remember { mutableLongStateOf(p.cellularWarnBytes) }
    var autoOwn by remember { mutableStateOf(p.autoAcceptOwn) }
    var alias by remember { mutableStateOf(p.alias) }
    var picker by remember { mutableStateOf<Picker?>(null) }
    var editName by remember { mutableStateOf(false) }
    var local by remember { mutableLongStateOf(0L) }
    var usage by remember { mutableStateOf<JSONObject?>(null) }
    var tick by remember { mutableIntStateOf(0) }
    val profile = app.server.profile
    val ex = LocalExtra.current

    LaunchedEffect(tick, server) {
        local = withContext(Dispatchers.IO) { dirSize(ctx.cacheDir) + dirSize(ctx.filesDir) }
        usage = withContext(Dispatchers.IO) { runCatching { app.server.api()?.get("/api/v1/me/usage") }.getOrNull() }
    }

    val themes = listOf("system" to "System default", "light" to "Light", "dark" to "Dark")
    val methods = listOf("auto" to "Automatic — direct when possible", "direct" to "Direct only", "server" to "Always via server", "ask" to "Ask each time")
    val cells = listOf(10L, 50L, 200L, 1024L).map { (it * 1024 * 1024).toString() to "Above ${formatBytes(it * 1024 * 1024)}" }
    val notifies = listOf("all" to "Progress and results", "important" to "Results only", "minimal" to "Only incoming files and failures")

    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp)) {
        Text("Settings", style = MaterialTheme.typography.headlineMedium, modifier = Modifier.padding(top = 16.dp, bottom = 16.dp))

        // Identity card
        Panel(onClick = { editName = true }) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                DeviceAvatar(app.certs.fingerprint, "mobile", 60.dp)
                Spacer(Modifier.width(16.dp))
                Column(Modifier.weight(1f)) {
                    Text(alias, style = MaterialTheme.typography.titleLarge)
                    Text("Device ID ${app.certs.fingerprint.take(8)}…", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    Spacer(Modifier.height(6.dp))
                    if (profile?.signedIn == true) Chip(profile.email, if (server is ServerState.Online) ex.ok else MaterialTheme.colorScheme.onSurfaceVariant,
                        if (server is ServerState.Online) ex.okSoft else MaterialTheme.colorScheme.surfaceVariant, Icons.Rounded.Cloud)
                    else Chip("Not signed in to a server", icon = Icons.Rounded.Cloud)
                }
            }
        }

        SectionTitle("General")
        GroupCard(rows = listOf(
            { RowItem("Appearance", themes.first { it.first == theme }.second, Icons.Rounded.Palette, Color(0xFF7A3DF0),
                onClick = { picker = Picker("Appearance", themes, theme) { p.theme = it } }) },
            { RowItem("Device name", alias, Icons.Rounded.Badge, onClick = { editName = true }) },
            { RowItem("Notifications", notifies.first { it.first == notify }.second, Icons.Rounded.Notifications, Color(0xFFE0612B),
                onClick = { picker = Picker("Notifications", notifies, notify) { notify = it; p.notifyLevel = it } }) },
        ))

        SectionTitle("Transfers")
        GroupCard(rows = listOf(
            { RowItem("Transfer method", methods.first { it.first == method }.second, Icons.Rounded.Route,
                onClick = { picker = Picker("Transfer method for your devices", methods, method) { method = it; p.method = it } }) },
            { RowItem("Mobile data warning", cells.firstOrNull { it.first == cell.toString() }?.second ?: formatBytes(cell), Icons.Rounded.SignalCellularAlt, Color(0xFF0E9F8E),
                onClick = { picker = Picker("Ask before using mobile data", cells, cell.toString()) { cell = it.toLong(); p.cellularWarnBytes = cell } }) },
        ))

        SectionTitle("Privacy & security")
        GroupCard(rows = listOf(
            { RowItem("Auto-accept from my devices", "Files from your own account skip the prompt", Icons.Rounded.Sync, ex.ok) {
                Switch(autoOwn, { autoOwn = it; p.autoAcceptOwn = it })
            } },
            { RowItem("Trusted devices", "Nearby devices you chose to remember", Icons.Rounded.VerifiedUser, ex.ok, onClick = { nav.navigate("trusted") }) },
        ))

        SectionTitle("Server")
        GroupCard(rows = listOf(
            { RowItem("Servers", profile?.let { "${it.name} · ${it.url}" } ?: "Add your self-hosted Ferry server", Icons.Rounded.Cloud,
                onClick = { nav.navigate("servers") }) },
        ))

        SectionTitle("Storage")
        GroupCard(rows = buildList<@Composable () -> Unit> {
            add { RowItem("App data on this phone", "Received files live in Downloads/Ferry", Icons.Rounded.Storage, Color(0xFF1D8FD6), chevron = false) { TrailingValue(formatBytes(local)) } }
            usage?.let { u ->
                val q = u.optLong("quotaBytes")
                val used = u.optLong("usedBytes")
                add {
                    Column(Modifier.padding(end = 16.dp)) {
                        RowItem("Used on server", if (q > 0) "${formatBytes(used)} of ${formatBytes(q)}" else "${formatBytes(used)} · no limit", Icons.Rounded.Cloud, chevron = false)
                        if (q > 0) Progress(used.toFloat() / q, Modifier.padding(start = 70.dp, bottom = 14.dp), 6.dp)
                    }
                }
            }
            add { RowItem("Clear cache", null, Icons.Rounded.CleaningServices, Color(0xFF6B6B74), onClick = {
                ctx.cacheDir.listFiles()?.forEach { it.deleteRecursively() }; tick++; toast(ctx, "Cache cleared")
            }, chevron = false) }
        })

        SectionTitle("About")
        GroupCard(rows = listOf(
            { RowItem("Ferry for Android", "Version ${BuildConfig.VERSION_NAME} · LocalSend v2 compatible", Icons.Rounded.Info, chevron = false) },
        ))
        Spacer(Modifier.height(NavBarSpace))
    }

    picker?.let { pk -> ChoiceSheet(pk) { picker = null } }
    if (editName) {
        var v by remember { mutableStateOf(alias) }
        AlertDialog(
            onDismissRequest = { editName = false },
            icon = { IconBadge(Icons.Rounded.Badge, size = 52.dp) },
            title = { Text("Device name") },
            text = {
                Column {
                    Text("Shown to nearby devices. Your email and account are never broadcast.", style = MaterialTheme.typography.bodyMedium)
                    Spacer(Modifier.height(12.dp))
                    OutlinedTextField(v, { v = it.take(40) }, singleLine = true, shape = RoundedCornerShape(16.dp))
                }
            },
            confirmButton = { Button({ p.alias = v; alias = p.alias; app.discovery.announce(); editName = false }, enabled = v.isNotBlank()) { Text("Save") } },
            dismissButton = { TextButton({ editName = false }) { Text("Cancel") } },
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ChoiceSheet(p: Picker, onClose: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onClose, sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true), containerColor = MaterialTheme.colorScheme.surface) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(bottom = 16.dp)) {
            Text(p.title, style = MaterialTheme.typography.titleLarge, modifier = Modifier.padding(horizontal = 24.dp, vertical = 8.dp))
            p.options.forEach { (v, label) ->
                Row(Modifier.fillMaxWidth().clickable { p.onPick(v); onClose() }.padding(horizontal = 12.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                    RadioButton(v == p.selected, { p.onPick(v); onClose() })
                    Text(label, style = MaterialTheme.typography.bodyLarge)
                }
            }
        }
    }
}

private fun dirSize(f: java.io.File): Long = f.walkTopDown().filter { it.isFile }.sumOf { it.length() }

@Composable
fun ToggleRow(title: String, subtitle: String, value: Boolean, onChange: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().clickable { onChange(!value) }.padding(vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Text(subtitle, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Switch(value, onChange)
    }
}

@Composable
fun TrustedScreen(nav: NavHostController) {
    val app = FerryApp.app
    val trusted by app.prefs.trusted.collectAsState()
    Column(Modifier.fillMaxSize()) {
        ScreenHeader("Trusted devices", "Remembered by their cryptographic ID", onBack = { nav.popBackStack() })
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(20.dp), verticalArrangement = androidx.compose.foundation.layout.Arrangement.spacedBy(12.dp)) {
            if (trusted.isEmpty()) item {
                EmptyState(Icons.Rounded.VerifiedUser, "No trusted devices", "When you accept files, tick “Remember this device” to trust it. Trust follows the device's ID, not its name or network.")
            }
            items(trusted, key = { it.fingerprint }) { t ->
                Panel {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        DeviceAvatar(t.fingerprint, size = 48.dp)
                        Spacer(Modifier.width(14.dp))
                        Column(Modifier.weight(1f)) {
                            Text(t.alias, style = MaterialTheme.typography.titleSmall)
                            Text("ID ${t.fingerprint.take(8)}… · added ${relativeTime(t.addedAt)}", style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        IconButton({ app.prefs.saveTrusted(trusted.filter { it.fingerprint != t.fingerprint }) }) { Icon(Icons.Rounded.Delete, "Forget ${t.alias}") }
                    }
                    Spacer(Modifier.height(4.dp))
                    ToggleRow("Auto-accept", "Receive from this device without asking", t.autoAccept) { on ->
                        app.prefs.saveTrusted(trusted.map { if (it.fingerprint == t.fingerprint) it.copy(autoAccept = on) else it })
                    }
                }
            }
        }
    }
}
