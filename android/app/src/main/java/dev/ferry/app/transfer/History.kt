package dev.ferry.app.transfer

import android.content.Context
import dev.ferry.app.data.Transfer
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import org.json.JSONArray
import java.io.File

/** Finished transfers on this device (newest first). Clearing history never deletes files. */
class History(ctx: Context) {
    private val file = File(ctx.filesDir, "history.json")
    private val _items = MutableStateFlow(load())
    val items: StateFlow<List<Transfer>> = _items

    private fun load(): List<Transfer> = runCatching {
        val a = JSONArray(file.readText())
        (0 until a.length()).mapNotNull { runCatching { Transfer.from(a.getJSONObject(it)) }.getOrNull() }
    }.getOrDefault(emptyList())

    @Synchronized
    fun add(t: Transfer) {
        _items.value = (listOf(t) + _items.value.filter { it.id != t.id }).take(300)
        save()
    }

    @Synchronized
    fun remove(id: String) {
        _items.value = _items.value.filter { it.id != id }; save()
    }

    @Synchronized
    fun clear() {
        _items.value = emptyList(); save()
    }

    private fun save() {
        val tmp = File(file.path + ".tmp")
        tmp.writeText(JSONArray(_items.value.map { it.toJson() }).toString())
        tmp.renameTo(file)
    }
}
