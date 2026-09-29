package dev.ferry.app

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.core.content.IntentCompat
import androidx.lifecycle.lifecycleScope
import dev.ferry.app.transfer.Storage
import dev.ferry.app.ui.FerryRoot
import dev.ferry.app.ui.FerryTheme
import dev.ferry.app.ui.Nav
import dev.ferry.app.ui.SendQueue
import dev.ferry.app.ui.handleScanned
import dev.ferry.app.ui.toast
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        setContent {
            val theme by FerryApp.app.prefs.themeFlow.collectAsState()
            FerryTheme(theme) { FerryRoot() }
        }
        if (savedInstanceState == null) handle(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
    }

    /** Share-sheet entry point, deep links and notification taps. */
    private fun handle(intent: Intent?) {
        intent ?: return
        intent.getStringExtra("route")?.let { Nav.pending.value = it }
        when (intent.action) {
            Intent.ACTION_SEND, Intent.ACTION_SEND_MULTIPLE -> {
                val uris = Storage.sharedUris(this, intent)
                if (uris.isEmpty()) toast(this, "Nothing to send in that share.") else queue(uris)
            }
            Intent.ACTION_VIEW -> intent.data?.let { data ->
                val connect = { lifecycleScope.launch { handleScanned(data.toString())?.let { toast(this@MainActivity, it) } } }
                if (data.host == "peer") {
                    // Links can come from any web page: don't contact the address until the user agrees.
                    val where = data.getQueryParameter("h").orEmpty().ifEmpty { "an unknown address" }
                    android.app.AlertDialog.Builder(this)
                        .setTitle("Connect to a nearby device?")
                        .setMessage("A link asks Ferry to connect to the device at $where. Only continue if you opened this link on purpose.")
                        .setPositiveButton("Connect") { _, _ -> connect() }
                        .setNegativeButton("Cancel", null)
                        .show()
                } else connect()
            }
        }
    }

    private fun queue(uris: List<Uri>) {
        val files = uris.mapNotNull { Storage.describe(this, it) }
        if (files.size < uris.size) toast(this, "${uris.size - files.size} file(s) couldn't be opened. Try sharing them again from the other app.")
        if (files.isEmpty()) return
        SendQueue.add(files)
        Nav.pending.value = "send"
    }
}
