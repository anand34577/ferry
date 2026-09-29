package dev.ferry.app.tv

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Badge
import androidx.compose.material.icons.rounded.DeleteSweep
import androidx.compose.material.icons.rounded.Info
import androidx.compose.material.icons.rounded.Password
import androidx.compose.material.icons.rounded.Route
import androidx.compose.material.icons.rounded.Sync
import androidx.compose.material.icons.rounded.VerifiedUser
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Icon
import androidx.tv.material3.ListItem
import androidx.tv.material3.Switch
import androidx.tv.material3.Text
import dev.ferry.app.BuildConfig
import dev.ferry.app.FerryApp
import dev.ferry.app.data.TrustedDevice

private val methods = listOf("auto" to "Automatic — direct when possible", "direct" to "Direct only", "server" to "Always via server")

@Composable
fun SettingsScreen() {
    val app = FerryApp.app
    val p = app.prefs
    val trusted by p.trusted.collectAsState()
    var alias by remember { mutableStateOf(p.alias) }
    var requirePin by remember { mutableStateOf(p.requirePin && p.pin.isNotEmpty()) }
    var pin by remember { mutableStateOf(p.pin) }
    var autoOwn by remember { mutableStateOf(p.autoAcceptOwn) }
    var method by remember { mutableStateOf(p.method.takeIf { m -> methods.any { it.first == m } } ?: "auto") }
    var editName by remember { mutableStateOf(false) }
    var editPin by remember { mutableStateOf(false) }
    var forget by remember { mutableStateOf<TrustedDevice?>(null) }
    var confirmClear by remember { mutableStateOf(false) }

    Page("Settings") {
        LazyColumn(Modifier.fillMaxSize().widthIn(max = 700.dp), contentPadding = PaddingValues(bottom = 40.dp, start = 4.dp, end = 4.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)) {
            item { SectionTitle("This TV") }
            item {
                SettingRow("Device name", alias, Icons.Rounded.Badge, Modifier.focusRequester(rememberInitialFocus())) { editName = true }
            }
            item {
                SettingRow("Require a PIN", if (requirePin) "Senders must enter $pin before you're asked" else "Off — anyone nearby can ask to send",
                    Icons.Rounded.Password, trailing = { Switch(requirePin, null) }) {
                    if (requirePin) { requirePin = false; p.requirePin = false } else editPin = true
                }
            }
            if (requirePin) item { SettingRow("Change PIN", pin, Icons.Rounded.Password) { editPin = true } }
            item {
                SettingRow("Auto-accept from my devices", "Files from your own server account skip the prompt", Icons.Rounded.Sync, trailing = { Switch(autoOwn, null) }) {
                    autoOwn = !autoOwn; p.autoAcceptOwn = autoOwn
                }
            }
            item {
                SettingRow("Transfer method", methods.first { it.first == method }.second, Icons.Rounded.Route) {
                    method = methods[(methods.indexOfFirst { it.first == method } + 1) % methods.size].first; p.method = method
                }
            }

            item { SectionTitle("Trusted devices", Modifier.padding(top = 10.dp)) }
            if (trusted.isEmpty()) item {
                Text("None yet. When accepting a transfer, choose “Remember this device” to trust it.", style = androidx.tv.material3.MaterialTheme.typography.bodyMedium,
                    color = Tv.muted, modifier = Modifier.padding(start = 4.dp))
            }
            items(trusted, key = { it.fingerprint }) { t ->
                SettingRow(t.alias.ifEmpty { "Unnamed device" }, if (t.autoAccept) "Receives without asking · OK to change" else "Asks before receiving · OK to change",
                    Icons.Rounded.VerifiedUser, trailing = { Switch(t.autoAccept, null) }, onLongClick = { forget = t }) {
                    p.saveTrusted(trusted.map { if (it.fingerprint == t.fingerprint) it.copy(autoAccept = !t.autoAccept) else it })
                }
            }
            if (trusted.isNotEmpty()) item {
                Text("Long-press OK on a device to forget it.", style = androidx.tv.material3.MaterialTheme.typography.bodyMedium, color = Tv.muted,
                    modifier = Modifier.padding(start = 4.dp))
            }

            item { SectionTitle("Other", Modifier.padding(top = 10.dp)) }
            item { SettingRow("Clear transfer history", "Received files stay in Files", Icons.Rounded.DeleteSweep) { confirmClear = true } }
            item { SettingRow("Ferry for Android TV", "Version ${BuildConfig.VERSION_NAME} · works with Ferry and LocalSend", Icons.Rounded.Info) {} }
        }
    }

    if (editName) InputDialog("Device name", "Name", alias, hint = "Shown to phones and computers looking for this TV.", onDismiss = { editName = false }) { v ->
        editName = false
        p.alias = v.take(40); alias = p.alias; app.discovery.announce()
    }
    if (editPin) InputDialog("Set a PIN", "PIN (4–8 digits)", pin, hint = "Senders must type this before this TV shows their request.",
        keyboard = KeyboardType.NumberPassword, filter = { s -> s.filter { it.isDigit() }.take(8) }, onDismiss = { editPin = false }) { v ->
        editPin = false
        if (v.length < 4) toast(app, "Use at least 4 digits")
        else { pin = v; p.pin = v; p.requirePin = true; requirePin = true }
    }
    forget?.let { t ->
        ConfirmDialog("Forget ${t.alias}?", "It will need your approval again next time it sends.", "Forget", { forget = null }) {
            forget = null; p.saveTrusted(trusted.filter { it.fingerprint != t.fingerprint })
        }
    }
    if (confirmClear) ConfirmDialog("Clear history?", "Only the list is cleared. Received files stay in Files.", "Clear", { confirmClear = false }) {
        confirmClear = false; app.history.clear()
    }
}

@Composable
private fun SettingRow(
    title: String,
    subtitle: String,
    icon: ImageVector,
    modifier: Modifier = Modifier,
    trailing: (@Composable () -> Unit)? = null,
    onLongClick: (() -> Unit)? = null,
    onClick: () -> Unit,
) {
    ListItem(
        selected = false, onClick = onClick, onLongClick = onLongClick, modifier = modifier,
        headlineContent = { Text(title) },
        supportingContent = { Text(subtitle, maxLines = 2) },
        leadingContent = { Icon(icon, null, Modifier.size(22.dp)) },
        trailingContent = trailing,
    )
}
