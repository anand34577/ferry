package dev.ferry.app.tv

import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.tv.material3.MaterialTheme
import androidx.tv.material3.darkColorScheme

/** The TV app is always dark: it's watched in dim rooms and shares the screen with video. */
object Tv {
    val bg = Color(0xFF0E0F13)
    val surface = Color(0xFF1A1C23)
    val surfaceHi = Color(0xFF2A2D38)
    val primary = Color(0xFF7C95FF)
    val onPrimary = Color(0xFF0A1440)
    val text = Color(0xFFF2F3F7)
    val muted = Color(0xFFA3A8B8)
    val ok = Color(0xFF4CCF93)
    val warn = Color(0xFFF0B44C)
    val danger = Color(0xFFFF7A7A)
    val violet = Color(0xFFB08BFF)
    val brand = Brush.linearGradient(listOf(Color(0xFF2456F5), Color(0xFF6B4DFF), Color(0xFF9B5CFF)))
}

@Composable
fun FerryTvTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = darkColorScheme(
            primary = Tv.primary, onPrimary = Tv.onPrimary,
            background = Tv.bg, onBackground = Tv.text,
            surface = Tv.surface, onSurface = Tv.text,
            surfaceVariant = Tv.surfaceHi, onSurfaceVariant = Tv.muted,
            error = Tv.danger, border = Tv.surfaceHi,
        ),
    ) {
        // Text fields come from mobile Material 3 (Compose for TV has none): give them the same dark palette.
        androidx.compose.material3.MaterialTheme(
            colorScheme = androidx.compose.material3.darkColorScheme(
                primary = Tv.primary, onPrimary = Tv.onPrimary, background = Tv.bg, onBackground = Tv.text,
                surface = Tv.surface, onSurface = Tv.text, surfaceVariant = Tv.surfaceHi, onSurfaceVariant = Tv.muted, error = Tv.danger,
            ),
        ) {
            // Compose for TV's Text takes its colour from the nearest Surface; screens sit directly on the background.
            androidx.compose.runtime.CompositionLocalProvider(androidx.tv.material3.LocalContentColor provides Tv.text, content = content)
        }
    }
}
