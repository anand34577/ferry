package dev.ferry.app.tv

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.CloudOff
import androidx.compose.material.icons.rounded.CloudQueue
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.FolderOpen
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material3.OutlinedTextField
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.tv.material3.Icon
import androidx.tv.material3.ListItem
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.Text
import dev.ferry.app.FerryApp
import dev.ferry.app.data.ServerProfile
import dev.ferry.app.server.ApiException
import dev.ferry.app.server.ServerManager
import dev.ferry.app.server.ServerState
import dev.ferry.app.util.formatBytes
import dev.ferry.app.util.relativeTime
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONObject

@Composable
fun ServerScreen() {
    val app = FerryApp.app
    val ctx = LocalContext.current
    val scope = rememberCoroutineScope()
    val state by app.server.state.collectAsState()
    val activeId by app.prefs.activeServerId.collectAsState()
    val profile = app.prefs.servers.collectAsState().value.find { it.id == activeId }
    var adding by remember { mutableStateOf(false) }
    var confirmRemove by remember { mutableStateOf(false) }

    Page(profile?.name ?: "Server", profile?.let { p -> listOfNotNull(p.url, p.email.takeIf { it.isNotEmpty() && p.signedIn }).joinToString(" · ") }
        ?: "Optional: your self-hosted Ferry server", actions = {
        if (profile != null) {
            Action("Refresh", Icons.Rounded.Refresh, primary = false) { app.server.refresh() }
            if (profile.signedIn) Action("Sign out", Icons.AutoMirrored.Rounded.Logout, primary = false) { app.server.logout(profile) }
            else Action("Remove server", Icons.Rounded.Delete, primary = false) { confirmRemove = true }
        }
    }) {
        when (val s = state) {
            ServerState.None -> Column {
                Empty(Icons.Rounded.CloudQueue, "No server yet",
                    "Nearby transfers work without one. Add your Ferry server to download your files on this TV, send to your devices anywhere and create links.")
                Action("Add server", Icons.Rounded.Add, Modifier.padding(start = 4.dp).focusRequester(rememberInitialFocus())) { adding = true }
            }
            ServerState.Checking -> Text("Connecting…", style = MaterialTheme.typography.titleLarge, color = Tv.muted)
            is ServerState.Offline -> Column {
                Empty(Icons.Rounded.CloudOff, "Server unavailable", s.reason + "\nNearby transfers still work.")
                Action("Try again", Icons.Rounded.Refresh, Modifier.focusRequester(rememberInitialFocus())) { app.server.refresh() }
            }
            is ServerState.Incompatible -> Empty(Icons.Rounded.CloudOff, "Version mismatch", s.reason)
            is ServerState.SignedOut -> SignIn(profile!!, s.reason)
            is ServerState.Online -> ServerFiles()
        }
    }

    if (adding) InputDialog("Add your server", "Server address", hint = "For example files.example.com or 192.168.1.10:8080",
        confirm = "Add", keyboard = KeyboardType.Uri, onDismiss = { adding = false }) { url ->
        adding = false
        scope.launch {
            try {
                app.server.addServer(url, "")
            } catch (e: Exception) {
                toast(ctx, ServerManager.friendly(e))
            }
        }
    }
    if (confirmRemove && profile != null) ConfirmDialog("Remove ${profile.name}?", "This TV forgets the server. Nothing is deleted on it.", "Remove",
        { confirmRemove = false }) { confirmRemove = false; app.server.remove(profile) }
}

@Composable
private fun SignIn(p: ServerProfile, reason: String) {
    val app = FerryApp.app
    val scope = rememberCoroutineScope()
    var email by remember { mutableStateOf(p.email) }
    var pw by remember { mutableStateOf("") }
    var code by remember { mutableStateOf("") }
    var needCode by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf("") }
    Panel(Modifier.width(520.dp)) {
        Text("Sign in", style = MaterialTheme.typography.titleLarge)
        Text(reason, style = MaterialTheme.typography.bodyMedium, color = Tv.muted)
        if (p.url.startsWith("http://")) Text("This server doesn't use HTTPS. Only sign in on a network you trust.", Modifier.padding(top = 8.dp),
            style = MaterialTheme.typography.bodyLarge, color = Tv.warn)
        OutlinedTextField(email, { email = it }, Modifier.fillMaxWidth().padding(top = 12.dp).focusRequester(rememberInitialFocus()),
            label = { androidx.compose.material3.Text("Email") }, singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Email, imeAction = ImeAction.Next))
        OutlinedTextField(pw, { pw = it }, Modifier.fillMaxWidth().padding(top = 12.dp), label = { androidx.compose.material3.Text("Password") }, singleLine = true,
            visualTransformation = PasswordVisualTransformation(), keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Done))
        if (needCode) OutlinedTextField(code, { code = it.filter(Char::isDigit).take(6) }, Modifier.fillMaxWidth().padding(top = 12.dp).focusRequester(rememberInitialFocus(needCode)),
            label = { androidx.compose.material3.Text("Two-factor code from your authenticator app") }, singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.NumberPassword, imeAction = ImeAction.Done))
        if (error.isNotEmpty()) Text(error, Modifier.padding(top = 10.dp), style = MaterialTheme.typography.bodyLarge, color = Tv.danger)
        Action(if (busy) "Signing in…" else "Sign in", modifier = Modifier.padding(top = 14.dp),
            enabled = !busy && email.isNotBlank() && pw.isNotEmpty() && (!needCode || code.length == 6)) {
            busy = true; error = ""
            scope.launch {
                try {
                    app.server.login(p, email.trim(), pw, if (needCode) code else "")
                } catch (e: Exception) {
                    if (e is ApiException && e.code == "totp_required") needCode = true else error = ServerManager.friendly(e)
                    if (e is ApiException && e.code == "invalid_code") code = ""
                } finally {
                    busy = false
                }
            }
        }
    }
}

/** Browse the server's files and download them to this TV. */
@Composable
private fun ServerFiles() {
    val app = FerryApp.app
    val ctx = LocalContext.current
    var folder by remember { mutableStateOf("") }
    var crumbs by remember { mutableStateOf<List<Pair<String, String>>>(emptyList()) }
    var folders by remember { mutableStateOf<List<JSONObject>>(emptyList()) }
    var files by remember { mutableStateOf<List<JSONObject>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf("") }
    var reload by remember { mutableIntStateOf(0) }
    var download by remember { mutableStateOf<JSONObject?>(null) }

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
    // Back goes up one folder before leaving the screen.
    BackHandler(enabled = folder.isNotEmpty()) { folder = crumbs.dropLast(1).lastOrNull()?.first.orEmpty() }

    Text((listOf("My files") + crumbs.map { it.second }).joinToString("  ›  "), Modifier.padding(bottom = 12.dp),
        style = MaterialTheme.typography.titleMedium, color = Tv.muted)
    if (error.isNotEmpty()) Text(error, style = MaterialTheme.typography.bodyLarge, color = Tv.danger)
    if (!loading && folders.isEmpty() && files.isEmpty() && error.isEmpty()) Empty(Icons.Rounded.FolderOpen, "This folder is empty",
        "Upload files to your server from your phone or computer to watch them here.")
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 40.dp, start = 4.dp, end = 4.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        items(folders, key = { "d" + it.getString("id") }) { f ->
            ListItem(selected = false, onClick = { folder = f.getString("id") },
                headlineContent = { Text(f.getString("name"), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                leadingContent = { val (i, c) = fileIcon("", "", folder = true); Icon(i, null, Modifier.size(24.dp), tint = c) })
        }
        items(files, key = { "f" + it.getString("id") }) { f ->
            ListItem(selected = false, onClick = { download = f },
                headlineContent = { Text(f.getString("name"), maxLines = 1, overflow = TextOverflow.Ellipsis) },
                supportingContent = { Text("${formatBytes(f.getLong("size"))} · ${relativeTime(f.getLong("updatedAt"), System.currentTimeMillis() + app.server.skewMs)}") },
                leadingContent = { val (i, c) = fileIcon(f.getString("name"), f.optString("mime")); Icon(i, null, Modifier.size(24.dp), tint = c) },
                trailingContent = { Icon(Icons.Rounded.Download, "Download") })
        }
    }
    download?.let { f ->
        ConfirmDialog("Download “${f.getString("name")}”?", "${formatBytes(f.getLong("size"))} is saved to this TV. It appears in Files when it's done.", "Download",
            { download = null }) {
            download = null
            app.transfers.downloadFromServer(listOf(f), app.server.profile?.name ?: "Server")
            toast(ctx, "Downloading — see Transfers")
        }
    }
}
