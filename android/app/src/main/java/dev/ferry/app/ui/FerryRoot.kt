package dev.ferry.app.ui

import android.Manifest
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.CloudQueue
import androidx.compose.material.icons.rounded.Home
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.rounded.SwapVert
import androidx.compose.material.icons.rounded.VerifiedUser
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SheetValue
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import dev.ferry.app.FerryApp
import dev.ferry.app.data.Method
import dev.ferry.app.data.TFile
import dev.ferry.app.util.formatBytes
import kotlinx.coroutines.flow.MutableStateFlow

/** Files queued for sending (from the picker or another app's share sheet). */
object SendQueue {
    val files = MutableStateFlow<List<TFile>>(emptyList())
    fun add(list: List<TFile>) {
        files.value = (files.value + list).distinctBy { it.uri }
    }
}

/** Deep links / notifications ask the UI to navigate somewhere. */
object Nav {
    val pending = MutableStateFlow<String?>(null)
}

private val TABS = setOf("home", "transfers", "server", "settings")

@Composable
fun FerryRoot() {
    val nav = rememberNavController()
    val app = FerryApp.app
    val active by app.transfers.active.collectAsState()
    val back by nav.currentBackStackEntryAsState()
    val route = back?.destination?.route ?: "home"
    val pendingRoute by Nav.pending.collectAsState()

    LaunchedEffect(pendingRoute) {
        pendingRoute?.let { r -> Nav.pending.value = null; nav.navigate(r) { launchSingleTop = true } }
    }
    // Android 13+: notifications are needed for the accept/decline prompt and background progress.
    val notifPerm = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) {}
    LaunchedEffect(Unit) {
        if (Build.VERSION.SDK_INT >= 33) notifPerm.launch(Manifest.permission.POST_NOTIFICATIONS)
    }

    Scaffold(
        containerColor = MaterialTheme.colorScheme.background,
        bottomBar = {
            if (route in TABS) FloatingNavBar(
                listOf(
                    NavTab("home", "Home", Icons.Rounded.Home),
                    NavTab("transfers", "Transfers", Icons.Rounded.SwapVert, active.count { it.status.active }),
                    NavTab("server", "Server", Icons.Rounded.CloudQueue),
                    NavTab("settings", "Settings", Icons.Rounded.Settings),
                ),
                route,
            ) { r -> if (r != route) nav.navigate(r) { popUpTo("home"); launchSingleTop = true } }
        },
    ) { pad ->
        NavHost(
            nav, startDestination = "home",
            // Tab screens scroll behind the floating nav bar; they reserve space at the end of their content.
            modifier = if (route in TABS) Modifier.padding(top = pad.calculateTopPadding()) else Modifier.padding(pad),
            enterTransition = { fadeIn(androidx.compose.animation.core.tween(180)) },
            exitTransition = { fadeOut(androidx.compose.animation.core.tween(120)) },
        ) {
            composable("home") { HomeScreen(nav) }
            composable("send") { SendScreen(nav) }
            composable("receive") { ReceiveScreen(nav) }
            composable("transfers") { TransfersScreen(nav) }
            composable("server") { ServerScreen(nav) }
            composable("servers") { ServersScreen(nav) }
            composable("settings") { SettingsScreen(nav) }
            composable("trusted") { TrustedScreen(nav) }
            composable("scan") { QrScanScreen(nav) }
        }
    }
    IncomingSheet()
    PinDialog()
}

/**
 * Explicit approval for unsolicited transfers with trust status front and centre.
 * A bottom sheet that can't be swiped away: the user must choose.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun IncomingSheet() {
    val app = FerryApp.app
    val queue by app.transfers.incoming.collectAsState()
    val req = queue.firstOrNull() ?: return
    var trust by remember(req.id) { mutableStateOf(false) }
    val state = rememberModalBottomSheetState(skipPartiallyExpanded = true, confirmValueChange = { it != SheetValue.Hidden })
    val ex = LocalExtra.current
    val unknown = !req.trusted && !req.ownAccount

    ModalBottomSheet(onDismissRequest = {}, sheetState = state, containerColor = MaterialTheme.colorScheme.surface, dragHandle = null) {
        Column(Modifier.fillMaxWidth().navigationBarsPadding().padding(horizontal = 24.dp, vertical = 20.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            RadarPulse(true, color = if (unknown) ex.warn else MaterialTheme.colorScheme.primary, size = 150.dp) {
                DeviceAvatar(req.fingerprint.ifEmpty { req.alias }, if (req.ownAccount) "desktop" else "mobile", 72.dp)
            }
            Text("${req.alias}", style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
            Text("wants to send you ${req.files.size} file${if (req.files.size == 1) "" else "s"} · ${formatBytes(req.total)}",
                style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center)
            Spacer(Modifier.height(12.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                when {
                    req.ownAccount -> Chip("Your account", ex.ok, ex.okSoft, Icons.Rounded.VerifiedUser)
                    req.trusted -> Chip("Trusted device", ex.ok, ex.okSoft, Icons.Rounded.VerifiedUser)
                    else -> Chip("Unknown device", ex.warn, ex.warnSoft, Icons.Rounded.Warning)
                }
                Chip(if (req.source == Method.DIRECT) "Direct · Wi-Fi" else "Via your server")
            }
            if (req.model.isNotEmpty() || req.fingerprint.isNotEmpty()) Text(
                listOfNotNull(req.model.ifEmpty { null }, req.fingerprint.takeIf { it.isNotEmpty() }?.let { "ID ${it.take(6)}…${it.takeLast(4)}" }).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 8.dp),
            )
            if (unknown) {
                Spacer(Modifier.height(14.dp))
                Hint("Only accept if you know who's sending. Being on the same Wi-Fi doesn't make a device trustworthy.", Icons.Rounded.Warning, ex.warn)
            }
            Spacer(Modifier.height(14.dp))
            Column(Modifier.fillMaxWidth().heightIn(max = 220.dp).verticalScroll(rememberScrollState())) {
                req.files.forEach { (n, s) ->
                    Row(Modifier.fillMaxWidth().padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                        FileIcon(n, size = 36.dp); Spacer(Modifier.width(12.dp))
                        Text(n, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium)
                        Text(formatBytes(s), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
            if (req.fingerprint.isNotEmpty() && !req.trusted) Row(Modifier.fillMaxWidth().clickable { trust = !trust }, verticalAlignment = Alignment.CenterVertically) {
                Checkbox(trust, { trust = it }); Text("Remember this device as trusted", style = MaterialTheme.typography.bodyMedium)
            }
            Spacer(Modifier.height(16.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                BigButton("Decline", modifier = Modifier.weight(1f), tonal = true) { app.transfers.respond(req.id, false, false) }
                BigButton("Accept", modifier = Modifier.weight(1f)) { app.transfers.respond(req.id, true, trust) }
            }
        }
    }
}

@Composable
fun PinDialog() {
    val app = FerryApp.app
    val req by app.transfers.pinRequest.collectAsState()
    val r = req ?: return
    var pin by remember(r) { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = { app.transfers.answerPin(null) },
        title = { Text("PIN required") },
        text = {
            Column {
                Text(if (r.wrong) "That PIN was wrong. Ask the person on ${r.peer} for the PIN shown on their screen." else "${r.peer} requires a PIN to receive files.")
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(pin, { pin = it.take(12) }, label = { Text("PIN") }, singleLine = true, shape = MaterialTheme.shapes.medium,
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword))
            }
        },
        confirmButton = { Button({ app.transfers.answerPin(pin) }, enabled = pin.isNotEmpty()) { Text("Send") } },
        dismissButton = { TextButton({ app.transfers.answerPin(null) }) { Text("Cancel") } },
    )
}
