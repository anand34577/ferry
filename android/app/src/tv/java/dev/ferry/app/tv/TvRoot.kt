package dev.ferry.app.tv

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.focusGroup
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Send
import androidx.compose.material.icons.rounded.CloudQueue
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.rounded.SwapVert
import androidx.compose.material.icons.rounded.VerifiedUser
import androidx.compose.material.icons.rounded.VideoLibrary
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.ExperimentalComposeUiApi
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalWindowInfo
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.tv.material3.Checkbox
import androidx.tv.material3.DrawerValue
import androidx.tv.material3.Icon
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.ModalNavigationDrawer
import androidx.tv.material3.NavigationDrawerItem
import androidx.tv.material3.Surface
import androidx.tv.material3.SurfaceDefaults
import androidx.tv.material3.Text
import dev.ferry.app.FerryApp
import dev.ferry.app.data.Method
import dev.ferry.app.ui.Nav
import dev.ferry.app.util.formatBytes
import kotlinx.coroutines.delay

enum class Screen(val label: String, val icon: ImageVector) {
    RECEIVE("Receive", Icons.Rounded.Download),
    FILES("Files", Icons.Rounded.VideoLibrary),
    SEND("Send", Icons.AutoMirrored.Rounded.Send),
    TRANSFERS("Transfers", Icons.Rounded.SwapVert),
    SERVER("Server", Icons.Rounded.CloudQueue),
    SETTINGS("Settings", Icons.Rounded.Settings),
}

@OptIn(ExperimentalComposeUiApi::class)
@Composable
fun TvRoot() {
    val app = FerryApp.app
    var screen by rememberSaveable { mutableStateOf(Screen.RECEIVE) }
    var viewing by remember { mutableStateOf<Pair<LocalFile, List<LocalFile>>?>(null) }
    val pending by Nav.pending.collectAsState()
    val active by app.transfers.active.collectAsState()
    val itemFocus = remember { List(Screen.entries.size) { FocusRequester() } }
    val contentFocus = remember { FocusRequester() }
    val incoming by app.transfers.incoming.collectAsState()
    val pin by app.transfers.pinRequest.collectAsState()

    // When a prompt or the viewer closes, put focus back on the page (otherwise it falls into the drawer and opens it).
    // Dialogs are separate windows: wait until this window has focus again before moving it.
    val overlay = incoming.isNotEmpty() || pin != null || viewing != null
    val windowFocused = LocalWindowInfo.current.isWindowFocused
    var restore by remember { mutableStateOf(false) }
    var contentHasFocus by remember { mutableStateOf(false) }
    LaunchedEffect(overlay, windowFocused) {
        if (overlay) restore = true
        else if (restore && windowFocused) {
            delay(250) // screens restore their own last-used item first (e.g. the file card that was playing)
            if (!contentHasFocus) runCatching { contentFocus.requestFocus() }
            restore = false
        }
    }

    // Notification taps and the share sheet ask for a screen by its phone route name.
    LaunchedEffect(pending) {
        val r = pending ?: return@LaunchedEffect
        Nav.pending.value = null
        screen = when (r) {
            "send" -> Screen.SEND
            "transfers" -> Screen.TRANSFERS
            else -> Screen.RECEIVE
        }
    }
    // Back returns to Receive (the home screen); Back on Receive leaves the app.
    BackHandler(enabled = screen != Screen.RECEIVE) { screen = Screen.RECEIVE }

    ModalNavigationDrawer(
        drawerContent = { value ->
            // Entering the drawer lands on the current screen's item, not whichever item happens to be level with the focus.
            Column(Modifier.fillMaxHeight().background(Tv.bg).focusProperties { enter = { itemFocus[screen.ordinal] } }.focusGroup()
                .padding(vertical = 20.dp, horizontal = 12.dp), verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Row(Modifier.padding(start = 8.dp, bottom = 16.dp), verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.size(32.dp).clip(RoundedCornerShape(12.dp)).background(Tv.brand), contentAlignment = Alignment.Center) {
                        Icon(Icons.Rounded.Download, null, Modifier.size(18.dp), tint = androidx.compose.ui.graphics.Color.White)
                    }
                    if (value == DrawerValue.Open) {
                        Spacer(Modifier.width(12.dp))
                        Text("Ferry", style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                    }
                }
                Screen.entries.forEach { s ->
                    NavigationDrawerItem(
                        selected = s == screen,
                        onClick = { screen = s },
                        modifier = Modifier.focusRequester(itemFocus[s.ordinal]),
                        leadingContent = { Icon(s.icon, null) },
                        trailingContent = if (s == Screen.TRANSFERS && active.isNotEmpty()) ({ Text("${active.size}", color = Tv.primary) }) else null,
                    ) { Text(s.label) }
                }
            }
        },
    ) {
        // The collapsed drawer (icons) overlays the left edge: keep content clear of it.
        Box(Modifier.fillMaxSize().background(Tv.bg).padding(start = 88.dp).focusRequester(contentFocus).onFocusChanged { contentHasFocus = it.hasFocus }.focusGroup()) {
            val view: (LocalFile) -> Unit = { f -> viewing = f to listOf(f) }
            when (screen) {
                Screen.RECEIVE -> ReceiveScreen(view)
                Screen.FILES -> FilesScreen({ f, list -> viewing = f to list }) { screen = Screen.SEND }
                Screen.SEND -> SendScreen({ screen = Screen.FILES }) { screen = Screen.TRANSFERS }
                Screen.TRANSFERS -> TransfersScreen(view)
                Screen.SERVER -> ServerScreen()
                Screen.SETTINGS -> SettingsScreen()
            }
        }
    }
    viewing?.let { (f, list) -> MediaViewer(list, f) { viewing = null } }
    IncomingDialog()
    PinDialog()
}

/**
 * Someone wants to send files: the TV asks before accepting anything.
 * Focus starts on Decline for unknown devices, so a stray OK press never accepts a stranger's files.
 */
@Composable
private fun IncomingDialog() {
    val app = FerryApp.app
    val queue by app.transfers.incoming.collectAsState()
    val req = queue.firstOrNull() ?: return
    var trust by remember(req.id) { mutableStateOf(false) }
    val unknown = !req.trusted && !req.ownAccount
    Dialog({}, DialogProperties(dismissOnBackPress = false, dismissOnClickOutside = false, usePlatformDefaultWidth = false)) {
        Surface(Modifier.width(600.dp), shape = RoundedCornerShape(20.dp), colors = SurfaceDefaults.colors(containerColor = Tv.surface, contentColor = Tv.text)) {
            Column(Modifier.padding(24.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Avatar(req.fingerprint.ifEmpty { req.alias }, if (req.ownAccount) "desktop" else "mobile", 52.dp)
                    Column(Modifier.padding(start = 14.dp).weight(1f)) {
                        Text(req.alias, style = MaterialTheme.typography.headlineSmall, fontWeight = FontWeight.Bold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Text("wants to send ${req.files.size} file${if (req.files.size == 1) "" else "s"} · ${formatBytes(req.total)}",
                            style = MaterialTheme.typography.bodyLarge, color = Tv.muted)
                    }
                }
                Row(Modifier.padding(top = 10.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                    when {
                        req.ownAccount -> Pill("Your account", Tv.ok, Icons.Rounded.VerifiedUser)
                        req.trusted -> Pill("Trusted device", Tv.ok, Icons.Rounded.VerifiedUser)
                        else -> Pill("Unknown device", Tv.warn, Icons.Rounded.Warning)
                    }
                    Pill(if (req.source == Method.DIRECT) "Direct · same network" else "Via your server")
                }
                if (unknown) Text("Only accept if you know who's sending. Being on the same Wi-Fi doesn't make a device trustworthy.",
                    Modifier.padding(top = 8.dp), style = MaterialTheme.typography.bodyMedium, color = Tv.warn)
                Column(Modifier.padding(top = 10.dp).fillMaxWidth().heightIn(max = 110.dp).verticalScroll(rememberScrollState())) {
                    req.files.forEach { (n, s) ->
                        Row(Modifier.padding(vertical = 3.dp), verticalAlignment = Alignment.CenterVertically) {
                            val (icon, tint) = fileIcon(n, "")
                            Icon(icon, null, Modifier.size(20.dp), tint = tint)
                            Text(n, Modifier.weight(1f).padding(horizontal = 10.dp), style = MaterialTheme.typography.bodyMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(formatBytes(s), style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
                        }
                    }
                }
                if (req.fingerprint.isNotEmpty() && !req.trusted) {
                    Surface(onClick = { trust = !trust }, modifier = Modifier.padding(top = 6.dp),
                        shape = androidx.tv.material3.ClickableSurfaceDefaults.shape(RoundedCornerShape(14.dp)),
                        colors = androidx.tv.material3.ClickableSurfaceDefaults.colors(containerColor = Tv.surface, focusedContainerColor = Tv.surfaceHi)) {
                        Row(Modifier.padding(horizontal = 12.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                            Checkbox(trust, null)
                            Text("Remember this device as trusted", Modifier.padding(start = 10.dp), style = MaterialTheme.typography.bodyMedium)
                        }
                    }
                }
                Spacer(Modifier.height(14.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                    val focus = Modifier.focusRequester(rememberInitialFocus(req.id))
                    Action("Decline", primary = false, modifier = if (unknown) focus else Modifier) { app.transfers.respond(req.id, false, false) }
                    Action("Accept", modifier = if (unknown) Modifier else focus) { app.transfers.respond(req.id, true, trust) }
                }
            }
        }
    }
}

/** This TV is sending to a device that requires a PIN. */
@Composable
private fun PinDialog() {
    val app = FerryApp.app
    val req by app.transfers.pinRequest.collectAsState()
    val r = req ?: return
    InputDialog("PIN required", "PIN",
        hint = if (r.wrong) "That PIN was wrong. Check the PIN shown on ${r.peer}." else "${r.peer} requires a PIN to receive files.",
        confirm = "Send", keyboard = KeyboardType.NumberPassword, filter = { it.take(12) },
        onDismiss = { app.transfers.answerPin(null) }) { app.transfers.answerPin(it) }
}
