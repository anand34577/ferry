package dev.ferry.app.ui

import androidx.compose.material3.HorizontalDivider
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.background
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.ArrowBack
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.ChevronRight
import androidx.compose.material.icons.outlined.CloudOff
import androidx.compose.material.icons.outlined.CloudQueue
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.FolderOpen
import androidx.compose.material.icons.outlined.Link
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.Smartphone
import androidx.compose.material.icons.outlined.Upload
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.outlined.Warning
import androidx.compose.material.icons.outlined.Dns
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.navigation.NavHostController
import dev.ferry.app.FerryApp
import dev.ferry.app.data.ServerProfile
import dev.ferry.app.server.ApiException
import dev.ferry.app.server.ServerManager
import dev.ferry.app.server.ServerState
import dev.ferry.app.transfer.Storage
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.relativeTime
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServerScreen(nav: NavHostController) {
    val app = FerryApp.app
    val state by app.server.state.collectAsState()
    val activeId by app.prefs.activeServerId.collectAsState()
    val profile = app.prefs.servers.collectAsState().value.find { it.id == activeId }
    var tab by remember { mutableIntStateOf(0) }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader(profile?.name ?: "Server", profile?.url, actions = {
            IconButton({ app.server.refresh() }) { Icon(Icons.Outlined.Refresh, "Refresh") }
            IconButton({ nav.navigate("servers") }) { Icon(Icons.Outlined.Dns, "Manage servers") }
        })
        when (val s = state) {
            ServerState.None -> EmptyState(Icons.Outlined.CloudQueue, "No server yet",
                "Ferry works without a server for nearby transfers. Add your self-hosted server to share links and reach your devices anywhere.") {
                Button({ nav.navigate("servers") }) { Text("Add server") }
            }
            ServerState.Checking -> Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
            is ServerState.Offline -> EmptyState(Icons.Outlined.CloudOff, "Server unavailable", s.reason + "\n\nNearby (direct) transfers still work.") {
                OutlinedButton({ app.server.refresh() }) { Text("Try again") }
            }
            is ServerState.Incompatible -> EmptyState(Icons.Outlined.CloudOff, "Version mismatch", s.reason)
            is ServerState.SignedOut -> LoginForm(profile!!, s.reason)
            is ServerState.Online -> {
                Segmented(listOf("Files", "Links", "Devices"), tab, { tab = it }, Modifier.padding(horizontal = 20.dp, vertical = 4.dp))
                when (tab) {
                    0 -> FilesTab()
                    1 -> LinksTab()
                    else -> DevicesTab(nav)
                }
            }
        }
    }
}

@Composable
private fun LoginForm(p: ServerProfile, reason: String) {
    val app = FerryApp.app
    val scope = rememberCoroutineScope()
    var email by remember { mutableStateOf(p.email) }
    var pw by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }
    var needCode by remember { mutableStateOf(false) }
    var code by remember { mutableStateOf("") }
    val ctx = LocalContext.current
    Column(Modifier.fillMaxWidth().padding(20.dp), horizontalAlignment = Alignment.CenterHorizontally) {
        BrandMark(64.dp)
        Spacer(Modifier.height(16.dp))
        Text("Sign in to ${p.name}", style = MaterialTheme.typography.headlineSmall)
        Text(reason, color = MaterialTheme.colorScheme.onSurfaceVariant, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 4.dp, bottom = 16.dp))
        if (p.url.startsWith("http://")) {
            Hint("This server doesn't use HTTPS. Only sign in on a network you trust.", Icons.Outlined.Warning, LocalExtra.current.warn)
            Spacer(Modifier.height(12.dp))
        }
        Panel {
            OutlinedTextField(email, { email = it }, Modifier.fillMaxWidth(), label = { Text("Email") }, singleLine = true, shape = RoundedCornerShape(16.dp),
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email))
            Spacer(Modifier.height(10.dp))
            OutlinedTextField(pw, { pw = it }, Modifier.fillMaxWidth(), label = { Text("Password") }, singleLine = true, shape = RoundedCornerShape(16.dp),
                visualTransformation = PasswordVisualTransformation(), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password))
            if (needCode) {
                Spacer(Modifier.height(10.dp))
                OutlinedTextField(code, { code = it.filter(Char::isDigit).take(6) }, Modifier.fillMaxWidth(), label = { Text("Two-factor code") }, singleLine = true,
                    shape = RoundedCornerShape(16.dp), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword),
                    supportingText = { Text("The 6-digit code from your authenticator app") })
            }
            if (error.isNotEmpty()) Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 10.dp))
            Spacer(Modifier.height(16.dp))
            BigButton(if (busy) "Signing in…" else "Sign in", modifier = Modifier.fillMaxWidth(), enabled = !busy && email.isNotBlank() && pw.isNotEmpty() && (!needCode || code.length == 6)) {
                busy = true; error = ""
                scope.launch {
                    try {
                        app.server.login(p, email, pw, if (needCode) code else "")
                    } catch (e: Exception) {
                        if (e is ApiException && e.code == "totp_required") needCode = true
                        else error = ServerManager.friendly(e)
                        if (e is ApiException && e.code == "invalid_code") code = ""
                    } finally {
                        busy = false
                    }
                }
            }
            TextButton(onClick = {
                runCatching { ctx.startActivity(android.content.Intent(android.content.Intent.ACTION_VIEW, android.net.Uri.parse(p.url.trimEnd('/') + "/forgot"))) }
            }, modifier = Modifier.align(Alignment.CenterHorizontally)) { Text("Forgot password?") }
        }
    }
}

// ---------- files ----------

@Composable
private fun FilesTab() {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var folder by remember { mutableStateOf("") }
    var crumbs by remember { mutableStateOf<List<Pair<String, String>>>(emptyList()) }
    var folders by remember { mutableStateOf<List<JSONObject>>(emptyList()) }
    var files by remember { mutableStateOf<List<JSONObject>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf("") }
    var reload by remember { mutableIntStateOf(0) }
    var shareFor by remember { mutableStateOf<JSONObject?>(null) }
    var deleteFor by remember { mutableStateOf<JSONObject?>(null) }

    LaunchedEffect(folder, reload) {
        loading = true
        try {
            val r = withContext(Dispatchers.IO) { app.server.api()!!.list(folder) }
            val fa = r.getJSONArray("folders"); val fi = r.getJSONArray("files"); val bc = r.getJSONArray("breadcrumbs")
            folders = (0 until fa.length()).map { fa.getJSONObject(it) }
            files = (0 until fi.length()).map { fi.getJSONObject(it) }
            crumbs = (0 until bc.length()).map { bc.getJSONObject(it).let { o -> o.getString("id") to o.getString("name") } }
            error = ""
        } catch (e: Exception) {
            error = ServerManager.friendly(e)
        } finally {
            loading = false
        }
    }
    val upload = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        val list = uris.mapNotNull { Storage.describe(ctx, it) }
        if (list.isNotEmpty()) {
            app.transfers.uploadToFolder(list, folder, crumbs.lastOrNull()?.second ?: "My files") { reload++ }
            toast(ctx, "Uploading ${list.size} file(s) — see Transfers")
        }
    }

    Scaffold(containerColor = MaterialTheme.colorScheme.background, contentWindowInsets = androidx.compose.foundation.layout.WindowInsets(0), floatingActionButton = {
        ExtendedFloatingActionButton(modifier = Modifier.padding(bottom = NavBarSpace - 16.dp), shape = RoundedCornerShape(50),
            containerColor = MaterialTheme.colorScheme.primary, contentColor = MaterialTheme.colorScheme.onPrimary, onClick = { upload.launch(arrayOf("*/*")) }, icon = { Icon(Icons.Outlined.Upload, null) }, text = { Text("Upload") })
    }) { pad ->
        LazyColumn(Modifier.fillMaxSize().padding(pad).padding(horizontal = 20.dp)) {
            item {
                Row(Modifier.horizontalScroll(rememberScrollState()).padding(top = 4.dp, bottom = 8.dp), verticalAlignment = Alignment.CenterVertically) {
                    TextButton({ folder = "" }) { Text("My files") }
                    crumbs.forEach { (id, name) ->
                        Icon(Icons.Outlined.ChevronRight, null, Modifier.size(16.dp))
                        TextButton({ folder = id }) { Text(name, maxLines = 1) }
                    }
                }
            }
            if (error.isNotEmpty()) item { Text(error, color = MaterialTheme.colorScheme.error, modifier = Modifier.padding(8.dp)) }
            if (loading && folders.isEmpty() && files.isEmpty()) item { Box(Modifier.fillMaxWidth().padding(40.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator() } }
            if (!loading && folders.isEmpty() && files.isEmpty() && error.isEmpty()) item {
                EmptyState(Icons.Outlined.FolderOpen, "This folder is empty", "Upload files from your phone to keep them on your server.")
            }
            // One rounded group card, rendered lazily: each row draws its own slice of the card.
            val total = folders.size + files.size
            fun Modifier.slice(i: Int): Modifier {
                val r = 22.dp
                val shape = RoundedCornerShape(topStart = if (i == 0) r else 0.dp, topEnd = if (i == 0) r else 0.dp,
                    bottomStart = if (i == total - 1) r else 0.dp, bottomEnd = if (i == total - 1) r else 0.dp)
                return this.clip(shape)
            }
            itemsIndexed(folders, key = { _, f -> "d" + f.getString("id") }) { i, f ->
                Column(Modifier.slice(i).background(MaterialTheme.colorScheme.surface)) {
                    RowItem(f.getString("name"), null, leading = { FileIcon(f.getString("name"), folder = true) }, onClick = { folder = f.getString("id") })
                    if (i < total - 1) HorizontalDivider(Modifier.padding(start = 74.dp), color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f))
                }
            }
            itemsIndexed(files, key = { _, f -> "f" + f.getString("id") }) { j, f ->
                val i = folders.size + j
                var menu by remember { mutableStateOf(false) }
                Column(Modifier.slice(i).background(MaterialTheme.colorScheme.surface)) {
                    RowItem(
                        f.getString("name"),
                        "${formatBytes(f.getLong("size"))} · ${relativeTime(f.getLong("updatedAt"), System.currentTimeMillis() + app.server.skewMs)}",
                        leading = { FileIcon(f.getString("name"), f.optString("mime")) },
                        onClick = { menu = true },
                        trailing = {
                            Box {
                                IconButton({ menu = true }) { Icon(Icons.Outlined.MoreVert, "Actions for ${f.getString("name")}") }
                                DropdownMenu(menu, { menu = false }) {
                                    DropdownMenuItem({ Text("Download to phone") }, leadingIcon = { Icon(Icons.Outlined.Download, null) }, onClick = {
                                        menu = false
                                        app.transfers.downloadFromServer(listOf(f), app.server.profile?.name ?: "Server")
                                        toast(ctx, "Downloading to Downloads/Ferry")
                                    })
                                    DropdownMenuItem({ Text("Share link") }, leadingIcon = { Icon(Icons.Outlined.Link, null) }, onClick = { menu = false; shareFor = f })
                                    DropdownMenuItem({ Text("Delete") }, leadingIcon = { Icon(Icons.Outlined.Delete, null) }, onClick = { menu = false; deleteFor = f })
                                }
                            }
                        },
                    )
                    if (i < total - 1) HorizontalDivider(Modifier.padding(start = 74.dp), color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.6f))
                }
            }
            item { Spacer(Modifier.height(NavBarSpace + 80.dp)) }
        }
    }
    shareFor?.let { f ->
        LinkOptionsDialog(onDismiss = { shareFor = null }) { exp, max, pw ->
            shareFor = null
            scope.launch {
                try {
                    val url = withContext(Dispatchers.IO) { app.server.api()!!.createShare(listOf(f.getString("id")), emptyList(), exp, max, pw).getString("url") }
                    shareText(ctx, url)
                } catch (e: Exception) {
                    toast(ctx, ServerManager.friendly(e))
                }
            }
        }
    }
    deleteFor?.let { f ->
        AlertDialog(
            onDismissRequest = { deleteFor = null },
            title = { Text("Delete “${f.getString("name")}”?") },
            text = { Text("Links that include this file will stop working. This can't be undone.") },
            confirmButton = {
                Button({
                    deleteFor = null
                    scope.launch {
                        runCatching { withContext(Dispatchers.IO) { app.server.api()!!.delete("/api/v1/files/${f.getString("id")}") } }
                            .onFailure { toast(ctx, ServerManager.friendly(it)) }
                        reload++
                    }
                }) { Text("Delete") }
            },
            dismissButton = { TextButton({ deleteFor = null }) { Text("Cancel") } },
        )
    }
}

// ---------- links ----------

@Composable
private fun LinksTab() {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var shares by remember { mutableStateOf<List<JSONObject>>(emptyList()) }
    var reload by remember { mutableIntStateOf(0) }
    var error by remember { mutableStateOf("") }
    var newUpload by remember { mutableStateOf(false) }
    LaunchedEffect(reload) {
        try {
            val a = withContext(Dispatchers.IO) { app.server.api()!!.get("/api/v1/shares").getJSONArray("shares") }
            shares = (0 until a.length()).map { a.getJSONObject(it) }
            error = ""
        } catch (e: Exception) {
            error = ServerManager.friendly(e)
        }
    }
    fun act(block: suspend () -> Unit) = scope.launch {
        runCatching { withContext(Dispatchers.IO) { block() } }.onFailure { toast(ctx, ServerManager.friendly(it)) }
        reload++
    }
    LazyColumn(Modifier.fillMaxSize().padding(horizontal = 16.dp)) {
        item {
            OutlinedButton({ newUpload = true }, Modifier.padding(vertical = 12.dp)) { Icon(Icons.Outlined.Add, null); Spacer(Modifier.width(6.dp)); Text("New upload link") }
        }
        if (error.isNotEmpty()) item { Text(error, color = MaterialTheme.colorScheme.error) }
        if (shares.isEmpty() && error.isEmpty()) item { EmptyState(Icons.Outlined.Link, "No links yet", "Links you create from this phone or the web appear here.") }
        items(shares, key = { it.getString("id") }) { s ->
            val status = s.optString("status")
            Panel(Modifier.padding(bottom = 8.dp)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text(s.optString("name"), style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        val meta = if (s.optString("kind") == "upload") "${s.optInt("uploadCount")} received" else
                            "${s.optInt("downloadCount")}${if (s.optInt("maxDownloads") > 0) "/" + s.optInt("maxDownloads") else ""} downloads"
                        val exp = s.optLong("expiresAt").let { if (it > 0) " · expires ${relativeTime(it, System.currentTimeMillis() + app.server.skewMs)}" else "" }
                        Text(meta + exp, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    val ex = LocalExtra.current
                    if (status == "active") Chip("Active", ex.ok, ex.okSoft) else Chip(status.replaceFirstChar { it.uppercase() })
                }
                Row(horizontalArrangement = Arrangement.spacedBy(0.dp)) {
                    TextButton({ copyText(ctx, s.getString("url")) }) { Icon(Icons.Outlined.ContentCopy, null, Modifier.size(16.dp)); Spacer(Modifier.width(4.dp)); Text("Copy") }
                    TextButton({ shareText(ctx, s.getString("url")) }) { Icon(Icons.Outlined.Share, null, Modifier.size(16.dp)); Spacer(Modifier.width(4.dp)); Text("Share") }
                    if (!s.optBoolean("revoked")) TextButton({ act { app.server.api()!!.patch("/api/v1/shares/${s.getString("id")}", JSONObject().put("revoked", true)) } }) { Text("Disable") }
                    else TextButton({ act { app.server.api()!!.patch("/api/v1/shares/${s.getString("id")}", JSONObject().put("revoked", false)) } }) { Text("Enable") }
                    TextButton({ act { app.server.api()!!.delete("/api/v1/shares/${s.getString("id")}") } }) { Icon(Icons.Outlined.Delete, "Delete link", Modifier.size(16.dp)) }
                }
            }
        }
        item { Spacer(Modifier.height(NavBarSpace)) }
    }
    if (newUpload) {
        var name by remember { mutableStateOf("") }
        AlertDialog(
            onDismissRequest = { newUpload = false },
            title = { Text("New upload link") },
            text = {
                Column {
                    Text("Anyone with the link can upload files to your server — no account or app needed.", style = MaterialTheme.typography.bodyMedium)
                    OutlinedTextField(name, { name = it }, label = { Text("Name, e.g. Holiday photos") }, singleLine = true, modifier = Modifier.padding(top = 8.dp))
                }
            },
            confirmButton = {
                Button({
                    newUpload = false
                    scope.launch {
                        runCatching { withContext(Dispatchers.IO) { app.server.api()!!.createUploadLink(name.ifBlank { "Upload link" }, 7 * 86400L).getString("url") } }
                            .onSuccess { shareText(ctx, it) }.onFailure { toast(ctx, ServerManager.friendly(it)) }
                        reload++
                    }
                }) { Text("Create") }
            },
            dismissButton = { TextButton({ newUpload = false }) { Text("Cancel") } },
        )
    }
}

// ---------- devices ----------

@Composable
private fun DevicesTab(nav: NavHostController) {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    var devices by remember { mutableStateOf<List<JSONObject>>(emptyList()) }
    var reload by remember { mutableIntStateOf(0) }
    var revoke by remember { mutableStateOf<JSONObject?>(null) }
    var rename by remember { mutableStateOf<JSONObject?>(null) }
    LaunchedEffect(reload) {
        runCatching { withContext(Dispatchers.IO) { app.server.api()!!.get("/api/v1/devices").getJSONArray("devices") } }
            .onSuccess { a -> devices = (0 until a.length()).map { a.getJSONObject(it) } }
    }
    LazyColumn(Modifier.fillMaxSize().padding(horizontal = 16.dp)) {
        item { SectionTitle("Signed in to your account") }
        items(devices, key = { it.getString("id") }) { d ->
            Row(Modifier.fillMaxWidth().padding(vertical = 10.dp), verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Outlined.Smartphone, null, tint = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text(d.getString("name") + if (d.optBoolean("current")) " (this phone)" else "", style = MaterialTheme.typography.titleSmall)
                    Text(if (d.optBoolean("online")) "Receiving now" else "Last active ${relativeTime(d.optLong("lastSeen"), System.currentTimeMillis() + app.server.skewMs)}",
                        style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                StatusDot(d.optBoolean("online"))
                IconButton({ rename = d }) { Icon(Icons.Outlined.Edit, "Rename ${d.getString("name")}") }
                if (!d.optBoolean("current")) IconButton({ revoke = d }) { Icon(Icons.Outlined.Delete, "Remove ${d.getString("name")}") }
            }
        }
        item {
            SectionTitle("Paired on this phone")
            OutlinedButton({ nav.navigate("trusted") }) { Text("Manage trusted nearby devices") }
        }
    }
    rename?.let { d ->
        var name by remember(d) { mutableStateOf(d.getString("name")) }
        AlertDialog(
            onDismissRequest = { rename = null },
            title = { Text("Rename device") },
            text = { OutlinedTextField(name, { name = it.take(80) }, singleLine = true, label = { Text("Name") }) },
            confirmButton = {
                Button({
                    rename = null
                    scope.launch {
                        runCatching { withContext(Dispatchers.IO) { app.server.api()!!.patch("/api/v1/devices/${d.getString("id")}", JSONObject().put("name", name)) } }
                            .onFailure { toast(ctx, ServerManager.friendly(it)) }
                        if (d.optBoolean("current")) app.prefs.alias = name
                        reload++
                    }
                }, enabled = name.isNotBlank()) { Text("Save") }
            },
            dismissButton = { TextButton({ rename = null }) { Text("Cancel") } },
        )
    }
    revoke?.let { d ->
        AlertDialog(
            onDismissRequest = { revoke = null },
            title = { Text("Remove ${d.getString("name")}?") },
            text = { Text("It will be signed out and can no longer receive files through your server.") },
            confirmButton = {
                Button({
                    revoke = null
                    scope.launch {
                        runCatching { withContext(Dispatchers.IO) { app.server.api()!!.delete("/api/v1/devices/${d.getString("id")}") } }.onFailure { toast(ctx, ServerManager.friendly(it)) }
                        reload++
                    }
                }) { Text("Remove") }
            },
            dismissButton = { TextButton({ revoke = null }) { Text("Cancel") } },
        )
    }
}

// ---------- server profiles ----------

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ServersScreen(nav: NavHostController) {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val servers by app.prefs.servers.collectAsState()
    val activeId by app.prefs.activeServerId.collectAsState()
    var url by remember { mutableStateOf(PendingServer.url.value ?: "") }
    var name by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }
    var removeP by remember { mutableStateOf<ServerProfile?>(null) }
    LaunchedEffect(Unit) { PendingServer.url.value = null }

    Column(Modifier.fillMaxSize()) {
        ScreenHeader("Servers", "Your self-hosted Ferry servers", onBack = { nav.popBackStack() })
        LazyColumn(Modifier.fillMaxSize().padding(horizontal = 20.dp)) {
            if (servers.isNotEmpty()) item {
                SectionTitle("Your servers")
                GroupCard(rows = servers.map { p ->
                    {
                        RowItem(
                            p.name, p.url + if (p.signedIn) " · ${p.email}" else " · signed out",
                            leading = { RadioButton(p.id == activeId, { app.server.switchTo(p.id) }) },
                            onClick = { app.server.switchTo(p.id) },
                            trailing = {
                                Row {
                                    if (p.signedIn) TextButton({ app.server.logout(p) }) { Text("Sign out") }
                                    IconButton({ removeP = p }) { Icon(Icons.Outlined.Delete, "Remove ${p.name}") }
                                }
                            },
                        )
                    }
                })
            }
            item {
                SectionTitle("Add a server")
                Panel {
                    OutlinedTextField(url, { url = it }, Modifier.fillMaxWidth(), label = { Text("Server address") }, placeholder = { Text("https://files.example.com") },
                        singleLine = true, shape = RoundedCornerShape(16.dp), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri))
                    Spacer(Modifier.height(10.dp))
                    OutlinedTextField(name, { name = it }, Modifier.fillMaxWidth(), label = { Text("Name (optional)") }, placeholder = { Text("Home server") }, singleLine = true,
                        shape = RoundedCornerShape(16.dp))
                    if (error.isNotEmpty()) Text(error, color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(top = 10.dp))
                    Spacer(Modifier.height(16.dp))
                    BigButton(if (busy) "Checking…" else "Test & add", modifier = Modifier.fillMaxWidth(), enabled = !busy && url.isNotBlank()) {
                        busy = true; error = ""
                        scope.launch {
                            try {
                                app.server.addServer(url, name)
                                url = ""; name = ""
                                toast(ctx, "Server added — sign in to continue")
                                nav.navigate("server") { popUpTo("home") }
                            } catch (e: Exception) {
                                error = ServerManager.friendly(e)
                            } finally {
                                busy = false
                            }
                        }
                    }
                    Spacer(Modifier.height(10.dp))
                    BigButton("Scan server QR code", Icons.Outlined.QrCodeScanner, Modifier.fillMaxWidth(), tonal = true) { nav.navigate("scan") }
                }
                Text("On the web app, open Devices to see a QR code for this server.", style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(vertical = 14.dp))
            }
        }
    }
    removeP?.let { p ->
        AlertDialog(
            onDismissRequest = { removeP = null },
            title = { Text("Remove ${p.name}?") },
            text = { Text("You'll be signed out on this phone. Files on the server are not affected.") },
            confirmButton = { Button({ app.server.remove(p); removeP = null }) { Text("Remove") } },
            dismissButton = { TextButton({ removeP = null }) { Text("Cancel") } },
        )
    }
}

/** A server address handed over by a QR code / deep link, prefilled in the Servers screen. */
object PendingServer {
    val url = kotlinx.coroutines.flow.MutableStateFlow<String?>(null)
}
