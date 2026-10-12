package com.supermetry

import java.io.File

/** Small persisted key/value store (SharedPreferences on Android). */
interface KeyValueStore {
    fun getString(key: String): String?
    fun putString(key: String, value: String?)
    fun getBoolean(key: String): Boolean
    fun putBoolean(key: String, value: Boolean)
}

/** Persistence for the event queue. */
interface QueueStore {
    fun load(): List<QueuedEvent>
    fun save(events: List<QueuedEvent>)
}

class InMemoryKeyValueStore : KeyValueStore {
    private val values = HashMap<String, Any>()

    @Synchronized override fun getString(key: String): String? = values[key] as? String

    @Synchronized override fun putString(key: String, value: String?) {
        if (value == null) values.remove(key) else values[key] = value
    }

    @Synchronized override fun getBoolean(key: String): Boolean = values[key] as? Boolean ?: false

    @Synchronized override fun putBoolean(key: String, value: Boolean) {
        values[key] = value
    }
}

class InMemoryQueueStore : QueueStore {
    @Volatile private var events: List<QueuedEvent> = emptyList()
    override fun load(): List<QueuedEvent> = events
    override fun save(events: List<QueuedEvent>) {
        this.events = ArrayList(events)
    }
}

/**
 * Line-based queue file: `v1<TAB>attempts<TAB>ts<TAB>id<TAB>json`. The JSON body is written
 * by [Json], which escapes every control character, so it never contains a tab or newline.
 * Writes go to a temp file that is renamed over the queue file.
 */
class FileQueueStore(private val file: File) : QueueStore {
    @Synchronized override fun load(): List<QueuedEvent> {
        if (!file.exists()) return emptyList()
        return try {
            file.readLines(Charsets.UTF_8).mapNotNull { line ->
                val parts = line.split('\t', limit = 5)
                if (parts.size != 5 || parts[0] != "v1") return@mapNotNull null
                val attempts = parts[1].toIntOrNull() ?: return@mapNotNull null
                val ts = parts[2].toLongOrNull() ?: return@mapNotNull null
                QueuedEvent(id = parts[3], ts = ts, body = parts[4], attempts = attempts)
            }
        } catch (_: Exception) {
            emptyList()
        }
    }

    @Synchronized override fun save(events: List<QueuedEvent>) {
        try {
            if (events.isEmpty()) {
                file.delete()
                return
            }
            file.parentFile?.mkdirs()
            val tmp = File(file.path + ".tmp")
            tmp.writeText(
                events.joinToString(separator = "\n", postfix = "\n") { "v1\t${it.attempts}\t${it.ts}\t${it.id}\t${it.body}" },
                Charsets.UTF_8,
            )
            if (!tmp.renameTo(file)) {
                file.delete()
                tmp.renameTo(file)
            }
        } catch (_: Exception) {
            // Persistence is best effort; the in-memory queue still works.
        }
    }
}
