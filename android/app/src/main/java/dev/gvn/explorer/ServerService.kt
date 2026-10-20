package dev.gvn.explorer

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.os.Build
import android.os.IBinder
import java.io.BufferedReader
import java.io.File
import java.io.InputStreamReader
import java.net.NetworkInterface

/**
 * Keeps the gvn-explorer server alive. The binary is our ordinary
 * CGO_ENABLED=0 build shipped as jniLibs/<abi>/libgvnserver.so: the
 * installer extracts it into the app's nativeLibraryDir, which is the one
 * location an app may exec from when targeting modern SDK levels (the
 * trick syncthing-android uses). The Go process inherits this app's UID,
 * so with "All files access" it can serve phone/SD/USB paths directly.
 *
 * perms.db deliberately stays in app-private storage: moving the shared
 * root to another volume never touches accounts/ACLs, and the file tree
 * on the volume stays the single source of truth for the files.
 */
class ServerService : Service() {

    companion object {
        const val ACTION_START = "dev.gvn.explorer.START"
        const val ACTION_STOP = "dev.gvn.explorer.STOP"
        private const val CHANNEL_ID = "gvn-server"
        private const val MAX_RETRIES = 5

        @Volatile
        private var proc: Process? = null
        private val logLines = ArrayList<String>()
        private val logLock = Any()

        fun isRunning(): Boolean = proc?.isAlive == true

        fun logTail(n: Int): String = synchronized(logLock) {
            logLines.drop(maxOf(0, logLines.size - n)).joinToString("\n")
        }

        fun pushLog(line: String) = synchronized(logLock) {
            logLines.add(line)
            if (logLines.size > 400) logLines.removeAt(0)
        }

        fun localAddresses(): List<String> = try {
            NetworkInterface.getNetworkInterfaces().asSequence()
                .flatMap { it.inetAddresses.asSequence() }
                .filter { !it.isLoopbackAddress && it.isSiteLocalAddress }
                .map { it.hostAddress ?: "" }
                .filter { it.isNotEmpty() && !it.contains(':') }
                .toList()
        } catch (e: Exception) {
            emptyList()
        }
    }

    private var wanted = false
    private var retries = 0

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        val nm = getSystemService(NotificationManager::class.java)
        if (Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL_ID, "File server", NotificationManager.IMPORTANCE_LOW)
            )
        }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            wanted = false
            stopProc()
            stopSelf()
            return START_NOT_STICKY
        }
        wanted = true
        retries = 0
        startForeground(1, note("gvn-explorer", "starting…"))
        startProc()
        // sticky so Doze/OEM killers that take the whole process down come back
        return START_STICKY
    }

    override fun onDestroy() {
        wanted = false
        stopProc()
    }

    private fun startProc() {
        val prefs = Prefs(this)
        val root = File(prefs.rootPath)
        if (!root.exists() && !root.mkdirs()) {
            pushLog("cannot create ${prefs.rootPath} — grant All-files access or pick another volume")
            updateNote("storage unavailable", "open the app to fix the storage location")
            return
        }
        val bin = File(applicationInfo.nativeLibraryDir, "libgvnserver.so")
        if (!bin.canExecute()) {
            pushLog("server binary missing from nativeLibraryDir (built via make android-apk?)")
            updateNote("binary missing", "open the app")
            return
        }
        val db = File(filesDir, "perms.db").absolutePath
        try {
            val pb = ProcessBuilder(
                bin.absolutePath,
                "-addr", ":${prefs.port}",
                "-root", prefs.rootPath,
                "-db", db
            )
            pb.redirectErrorStream(true)
            val p = pb.start()
            proc = p
            Thread {
                BufferedReader(InputStreamReader(p.inputStream)).use { r ->
                    while (true) {
                        val line = r.readLine() ?: break
                        pushLog(line)
                    }
                }
                val code = p.waitFor()
                proc = null
                if (wanted && code != 0 && retries < MAX_RETRIES) {
                    retries++
                    pushLog("server exited ($code), retry $retries/$MAX_RETRIES in 3s")
                    try {
                        Thread.sleep(3000)
                    } catch (e: InterruptedException) {
                        return@Thread
                    }
                    if (wanted) startProc()
                } else if (wanted) {
                    pushLog("server exited ($code) — giving up until you press Start")
                    updateNote("server died (exit $code)", "open the app")
                }
            }.start()
            updateNote("running", "https://" + localAddresses().firstOrNull() + ":${prefs.port}")
        } catch (e: Exception) {
            pushLog("start failed: $e")
            updateNote("start failed", "open the app")
        }
    }

    private fun stopProc() {
        try {
            proc?.destroy()
        } catch (e: Exception) {
            // already gone
        }
        proc = null
    }

    private fun updateNote(title: String, text: String) = updateNote(note(title, text))

    private fun updateNote(n: Notification) {
        try {
            val nm = getSystemService(NotificationManager::class.java)
            nm.notify(1, n)
        } catch (e: Exception) {
            // notification permission off: the service still runs
        }
    }

    private fun note(title: String, text: String): Notification {
        val b = if (Build.VERSION.SDK_INT >= 26) {
            Notification.Builder(this, CHANNEL_ID)
        } else {
            @Suppress("DEPRECATION")
            Notification.Builder(this)
        }
        return b.setSmallIcon(android.R.drawable.stat_sys_download_done)
            .setContentTitle(title)
            .setContentText(text)
            .build()
    }
}
