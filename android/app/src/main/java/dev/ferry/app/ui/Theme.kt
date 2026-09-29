package dev.ferry.app.ui

import androidx.compose.foundation.IndicationNodeFactory
import androidx.compose.foundation.LocalIndication
import androidx.compose.foundation.interaction.FocusInteraction
import androidx.compose.foundation.interaction.InteractionSource
import androidx.compose.material3.ripple
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.drawscope.ContentDrawScope
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.node.DelegatableNode
import androidx.compose.ui.node.DelegatingNode
import androidx.compose.ui.node.DrawModifierNode
import androidx.compose.ui.node.invalidateDraw
import kotlinx.coroutines.launch
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.Typography
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp

// Same palette as the web app (web/src/styles.css) so both clients feel like one product.
private val Light = lightColorScheme(
    primary = Color(0xFF2456F5), onPrimary = Color.White, primaryContainer = Color(0xFFE8EEFF), onPrimaryContainer = Color(0xFF12307F),
    secondary = Color(0xFF7A5CFF), onSecondary = Color.White,
    background = Color(0xFFF5F4F0), onBackground = Color(0xFF18181B),
    surface = Color(0xFFFFFFFF), onSurface = Color(0xFF18181B), surfaceVariant = Color(0xFFEAE8E2), onSurfaceVariant = Color(0xFF6B6B74),
    surfaceContainer = Color(0xFFFFFFFF), surfaceContainerHigh = Color(0xFFFAF9F7), surfaceContainerLow = Color(0xFFF5F4F0),
    outline = Color(0xFFD3D0C8), outlineVariant = Color(0xFFE4E2DC),
    error = Color(0xFFC0262D), errorContainer = Color(0xFFFDEAEA), onErrorContainer = Color(0xFF8A1A1F),
)
private val Dark = darkColorScheme(
    primary = Color(0xFF7090FF), onPrimary = Color(0xFF0B0D14), primaryContainer = Color(0xFF1C2340), onPrimaryContainer = Color(0xFFC9D5FF),
    secondary = Color(0xFFA18BFF), onSecondary = Color(0xFF0B0D14),
    background = Color(0xFF0E0F11), onBackground = Color(0xFFECECEF),
    surface = Color(0xFF17181B), onSurface = Color(0xFFECECEF), surfaceVariant = Color(0xFF222328), onSurfaceVariant = Color(0xFF9C9CA6),
    surfaceContainer = Color(0xFF17181B), surfaceContainerHigh = Color(0xFF1C1D21), surfaceContainerLow = Color(0xFF131417),
    outline = Color(0xFF383A41), outlineVariant = Color(0xFF2A2B31),
    error = Color(0xFFFF7B7F), errorContainer = Color(0xFF2D1415), onErrorContainer = Color(0xFFFFC9CB),
)

data class Extra(val ok: Color, val okSoft: Color, val warn: Color, val warnSoft: Color)
val LocalExtra = staticCompositionLocalOf { Extra(Color(0xFF0F7B4F), Color(0xFFE3F5EC), Color(0xFF9A5B00), Color(0xFFFFF1D6)) }

@Composable
fun FerryTheme(mode: String, content: @Composable () -> Unit) {
    val dark = when (mode) {
        "light" -> false; "dark" -> true; else -> isSystemInDarkTheme()
    }
    val base = Typography()
    val type = Typography(
        displaySmall = base.displaySmall.copy(fontWeight = FontWeight.Bold, letterSpacing = (-0.8).sp),
        headlineMedium = base.headlineMedium.copy(fontWeight = FontWeight.Bold, fontSize = 30.sp, letterSpacing = (-0.6).sp),
        headlineSmall = base.headlineSmall.copy(fontWeight = FontWeight.Bold, letterSpacing = (-0.3).sp),
        titleLarge = base.titleLarge.copy(fontWeight = FontWeight.Bold, letterSpacing = (-0.2).sp),
        titleMedium = base.titleMedium.copy(fontWeight = FontWeight.SemiBold, fontSize = 17.sp),
        titleSmall = base.titleSmall.copy(fontWeight = FontWeight.SemiBold, fontSize = 15.sp),
        labelLarge = base.labelLarge.copy(fontWeight = FontWeight.SemiBold),
        bodySmall = base.bodySmall.copy(fontSize = 12.5.sp),
    )
    CompositionLocalProvider(
        LocalExtra provides if (dark) Extra(Color(0xFF4CCF93), Color(0xFF10271D), Color(0xFFF0B44C), Color(0xFF2E2310))
        else Extra(Color(0xFF0F7B4F), Color(0xFFE3F5EC), Color(0xFF9A5B00), Color(0xFFFFF1D6))
    ) {
        MaterialTheme(
            colorScheme = if (dark) Dark else Light,
            typography = type,
            shapes = Shapes(small = RoundedCornerShape(10.dp), medium = RoundedCornerShape(14.dp), large = RoundedCornerShape(18.dp)),
            content = { CompositionLocalProvider(LocalIndication provides FocusRing(MaterialTheme.colorScheme.onBackground), content = content) },
        )
    }
}

/** Ripple plus a clear outline on D-pad/keyboard focus: the ripple's faint focus tint is invisible from a sofa. */
private class FocusRing(private val color: Color) : IndicationNodeFactory {
    override fun create(interactionSource: InteractionSource): DelegatableNode = Node(interactionSource, color)
    override fun equals(other: Any?) = other is FocusRing && other.color == color
    override fun hashCode() = color.hashCode()

    private class Node(private val source: InteractionSource, private val color: Color) : DelegatingNode(), DrawModifierNode {
        private var focused = false

        init { delegate(ripple().create(source)) }

        override fun onAttach() {
            coroutineScope.launch {
                source.interactions.collect {
                    if (it is FocusInteraction.Focus) focused = true else if (it is FocusInteraction.Unfocus) focused = false else return@collect
                    invalidateDraw()
                }
            }
        }

        override fun ContentDrawScope.draw() {
            drawContent()
            if (!focused) return
            // ponytail: shape-agnostic rounded ring; clickables are clipped to their own shape so it stays inside.
            val w = 3.dp.toPx()
            val r = minOf(24.dp.toPx(), size.minDimension / 2)
            drawRoundRect(color, Offset(w / 2, w / 2), Size(size.width - w, size.height - w), CornerRadius(r), Stroke(w))
        }
    }
}
