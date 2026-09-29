package dev.ferry.app

import android.content.Intent
import android.net.Uri
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.core.content.IntentCompat
import dev.ferry.app.transfer.Storage
import dev.ferry.app.tv.FerryTvTheme
import dev.ferry.app.tv.TvRoot
import dev.ferry.app.tv.toast
import dev.ferry.app.ui.Nav
import dev.ferry.app.ui.SendQueue

/** Ferry for Android TV. Same backend as the phone app (src/main); UI in dev.ferry.app.tv. */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val app = FerryApp.app
        // A TV's main job is receiving: start visible on first launch (it can be switched off on the Receive screen).
        if (!app.prefs.onboarded) {
            app.prefs.onboarded = true
            app.setReceiving(true)
        }
        setContent { FerryTvTheme { TvRoot() } }
        if (savedInstanceState == null) handle(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
    }

    /** Notification taps and files shared from other TV apps. */
    private fun handle(intent: Intent?) {
        intent ?: return
        intent.getStringExtra("route")?.let { Nav.pending.value = it }
        val uris = when (intent.action) {
            Intent.ACTION_SEND -> listOfNotNull(IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java))
            Intent.ACTION_SEND_MULTIPLE -> IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java).orEmpty()
            else -> return
        }
        val files = uris.mapNotNull { Storage.describe(this, it) }
        if (files.size < uris.size) toast(this, "${uris.size - files.size} file(s) couldn't be opened.")
        if (files.isEmpty()) return
        SendQueue.add(files)
        Nav.pending.value = "send"
    }
}
