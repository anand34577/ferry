package dev.ferry.app.ui

import android.net.Uri
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Badge
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.Password
import androidx.compose.material.icons.rounded.PlayArrow
import androidx.compose.material.icons.rounded.Stop
import androidx.compose.material.icons.rounded.VerifiedUser
import androidx.compose.material.icons.rounded.WifiOff
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.navigation.NavHostController
import dev.ferry.app.FerryApp
import dev.ferry.app.util.PairCode

@Composable
fun ReceiveScreen(nav: NavHostController) {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val receiving by app.receivingFlow.collectAsState()
    val net by app.net.state.collectAsState()
    val running by app.discovery.running.collectAsState()
    var alias by remember { mutableStateOf(app.prefs.alias) }
    var editName by remember { mutableStateOf(false) }
    var requirePin by remember { mutableStateOf(app.prefs.requirePin) }
    var pin by remember { mutableStateOf(app.prefs.pin.ifEmpty { (1000..9999).random().toString() }) }
    val ex = LocalExtra.current

    Column(Modifier.fillMaxSize()) {
        ScreenHeader("Receive", onBack = { nav.popBackStack() })
        Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp), horizontalAlignment = Alignment.CenterHorizontally) {
            RadarPulse(receiving && net.lan, color = if (receiving) ex.ok else MaterialTheme.colorScheme.primary, size = 210.dp) {
                DeviceAvatar(app.certs.fingerprint, "mobile", 92.dp)
            }
            Text(alias, style = MaterialTheme.typography.headlineSmall, textAlign = TextAlign.Center)
            Text(
                when {
                    !receiving -> "Nearby devices can't see you"
                    !net.lan -> "Waiting for a Wi-Fi or hotspot network"
                    else -> "Visible to nearby devices · you approve every transfer"
                },
                style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant, textAlign = TextAlign.Center,
            )
            Spacer(Modifier.height(18.dp))
            BigButton(if (receiving) "Stop receiving" else "Start receiving", if (receiving) Icons.Rounded.Stop else Icons.Rounded.PlayArrow,
                Modifier.fillMaxWidth(), tonal = receiving) { app.setReceiving(!receiving) }

            if (receiving && !net.lan) {
                Spacer(Modifier.height(16.dp))
                Hint("Join the same Wi-Fi as the sender, or turn on your hotspot and let them join it. No internet needed.", Icons.Rounded.WifiOff, ex.warn)
            }

            AnimatedVisibility(receiving && net.lan && running) {
                val ip = net.lanAddrs.firstOrNull() ?: ""
                val port = app.discovery.port
                val qr = "ferry://peer?h=" + Uri.encode(net.lanAddrs.joinToString(",")) + "&p=$port&f=" + app.certs.fingerprint + "&n=" + Uri.encode(app.prefs.alias) + "&s=https"
                val code = PairCode.encode(ip, port) ?: ip
                Column(Modifier.fillMaxWidth().padding(top = 22.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                    Panel(padding = 22.dp) {
                        Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally) {
                            Text("Scan to connect", style = MaterialTheme.typography.titleMedium)
                            Text("Or type the code in Ferry → Send → Connect", style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            Spacer(Modifier.height(16.dp))
                            QrImage(qr, 200.dp)
                            Spacer(Modifier.height(18.dp))
                            Row(horizontalArrangement = Arrangement.spacedBy(6.dp), verticalAlignment = Alignment.CenterVertically) {
                                code.forEach { c ->
                                    if (c == '-') Text("–", style = MaterialTheme.typography.titleLarge, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                    else Box(Modifier.width(34.dp).clip(RoundedCornerShape(10.dp)).background(MaterialTheme.colorScheme.surfaceVariant).padding(vertical = 8.dp),
                                        contentAlignment = Alignment.Center) {
                                        Text(c.toString(), fontFamily = FontFamily.Monospace, fontSize = 22.sp, fontWeight = FontWeight.Bold)
                                    }
                                }
                            }
                            Spacer(Modifier.height(10.dp))
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Text("IP $ip${if (port != PairCode.DEFAULT_PORT) ":$port" else ""}", style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant)
                                Spacer(Modifier.width(10.dp))
                                PillAction("Copy code", Icons.Rounded.ContentCopy) { copyText(ctx, code, "Pairing code") }
                            }
                        }
                    }
                    Text("Works with Ferry and LocalSend on the same network.", style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(top = 10.dp))
                }
            }

            SectionTitle("Preferences")
            GroupCard(rows = listOf(
                { RowItem("Device name", alias, Icons.Rounded.Badge, onClick = { editName = true }) },
                {
                    RowItem("Require a PIN", if (requirePin) "PIN $pin — senders must enter it first" else "Senders must enter a PIN before you're asked",
                        Icons.Rounded.Password, Color(0xFF7A3DF0)) {
                        Switch(requirePin, { requirePin = it; app.prefs.requirePin = it; if (it) app.prefs.pin = pin })
                    }
                },
                { RowItem("Trusted devices", "Devices you chose to remember", Icons.Rounded.VerifiedUser, ex.ok, onClick = { nav.navigate("trusted") }) },
                { RowItem("Saved to", "Downloads/Ferry", Icons.Rounded.Download, Color(0xFF0E9F8E), chevron = false) },
            ))
            if (requirePin) {
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(pin, { v -> pin = v.filter { it.isDigit() }.take(8); app.prefs.pin = pin }, Modifier.fillMaxWidth(),
                    label = { Text("PIN") }, singleLine = true, shape = RoundedCornerShape(16.dp), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number))
            }
            Text("Only your device name is broadcast — never your email or account. Files from your server arrive automatically while you're signed in.",
                style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(vertical = 18.dp))
        }
    }
    if (editName) {
        var v by remember { mutableStateOf(alias) }
        AlertDialog(
            onDismissRequest = { editName = false },
            title = { Text("Device name") },
            text = { OutlinedTextField(v, { v = it.take(40) }, singleLine = true, shape = RoundedCornerShape(16.dp)) },
            confirmButton = { Button({ app.prefs.alias = v; alias = app.prefs.alias; app.discovery.announce(); editName = false }, enabled = v.isNotBlank()) { Text("Save") } },
            dismissButton = { TextButton({ editName = false }) { Text("Cancel") } },
        )
    }
}
