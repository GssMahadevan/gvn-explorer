package dev.gvn.explorer

import android.content.Context
import android.os.Environment
import java.io.File

/** The three settings the app persists: shared root path, port, boot autostart. */
class Prefs(ctx: Context) {
    private val sp = ctx.getSharedPreferences("gvn", Context.MODE_PRIVATE)

    val rootPath: String
        get() = sp.getString(KEY_ROOT, null) ?: defaultRoot()

    fun setRoot(path: String) = sp.edit().putString(KEY_ROOT, path).apply()

    val port: Int
        get() = sp.getInt(KEY_PORT, DEFAULT_PORT)

    fun setPort(port: Int) = sp.edit().putInt(KEY_PORT, port).apply()

    val autostart: Boolean
        get() = sp.getBoolean(KEY_AUTOSTART, false)

    fun setAutostart(on: Boolean) = sp.edit().putBoolean(KEY_AUTOSTART, on).apply()

    companion object {
        const val DEFAULT_PORT = 8443
        private const val KEY_ROOT = "root"
        private const val KEY_PORT = "port"
        private const val KEY_AUTOSTART = "autostart"

        fun defaultRoot(): String =
            File(Environment.getExternalStorageDirectory(), "gvn").absolutePath
    }
}
