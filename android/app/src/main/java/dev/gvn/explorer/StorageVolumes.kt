package dev.gvn.explorer

import android.content.Context
import android.os.Build
import android.os.Environment
import android.os.storage.StorageManager
import java.io.File

data class StorageRoot(val label: String, val path: String)

/**
 * The volume picker behind the "Storage location" setting. The Go server
 * needs REAL paths (it is a plain filesystem process), so we list mount
 * points, not SAF URIs: primary external storage, plus whatever SD-card
 * or USB-OTG volumes show up under /storage or /mnt/media_rw. Writability
 * is probed so the picker says honestly what the phone will accept.
 */
object StorageVolumes {

    fun list(ctx: Context): List<StorageRoot> {
        val out = LinkedHashMap<String, StorageRoot>()

        fun add(path: String?, label: String) {
            if (path.isNullOrBlank()) return
            val p = path.trimEnd('/')
            if (p.length < 2 || out.containsKey(p)) return
            val writable = try {
                val d = File(p)
                (d.exists() && d.canWrite()) || (!d.exists() && File(File(p).parent ?: "/").canWrite())
            } catch (e: Exception) {
                false
            }
            out[p] = StorageRoot(if (writable) label else "$label (not writable)", p)
        }

        add(Environment.getExternalStorageDirectory()?.path, "Phone storage")

        // Secondary volumes via StorageManager (SD cards, USB sticks).
        try {
            if (Build.VERSION.SDK_INT >= 26) {
                val sm = ctx.getSystemService(Context.STORAGE_SERVICE) as StorageManager
                for (v in sm.storageVolumes) {
                    if (v.isPrimary) continue
                    val p = try {
                        if (Build.VERSION.SDK_INT >= 30) v.directory?.path
                        else v.javaClass.getMethod("getPath").invoke(v)?.toString()
                    } catch (e: Exception) {
                        null
                    }
                    val uuid = try {
                        v.javaClass.getMethod("getUuid").invoke(v)?.toString() ?: "volume"
                    } catch (e: Exception) {
                        "volume"
                    }
                    add(p, "Removable $uuid")
                }
            }
        } catch (e: Exception) {
            // older/OEM-weird devices: the probes below still cover them
        }

        // Probe the usual mount parents too (USB-OTG lands here on many ROMs).
        for (dir in arrayOf("/storage", "/mnt/media_rw")) {
            val kids = File(dir).listFiles() ?: continue
            for (k in kids.sortedBy { it.name }) {
                val n = k.name
                if (!k.isDirectory || n == "emulated" || n == "self" || n == "system" ||
                    n == "persistent" || n == "autosd"
                ) continue
                add(k.path, "Removable ${n.take(12)}")
            }
        }

        return out.values.toList()
    }
}
