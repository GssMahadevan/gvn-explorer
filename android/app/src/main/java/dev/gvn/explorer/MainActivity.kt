package dev.gvn.explorer

import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.text.InputType
import android.widget.ArrayAdapter
import android.widget.Button
import android.widget.CheckBox
import android.widget.EditText
import android.widget.Spinner
import android.widget.TextView
import android.widget.Toast

class MainActivity : Activity() {

    private lateinit var prefs: Prefs
    private lateinit var statusView: TextView
    private lateinit var logView: TextView
    private lateinit var rootPathView: EditText
    private lateinit var portView: EditText
    private lateinit var autostartView: CheckBox
    private lateinit var volumesView: Spinner
    private var volumeEntries: List<StorageRoot> = emptyList()

    private val refresh = object : Runnable {
        override fun run() {
            renderState()
            Handler(Looper.getMainLooper()).postDelayed(this, 2000)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.main)

        prefs = Prefs(this)
        statusView = findViewById(R.id.status)
        logView = findViewById(R.id.log)
        rootPathView = findViewById(R.id.root_path)
        portView = findViewById(R.id.port)
        autostartView = findViewById(R.id.autostart)
        volumesView = findViewById(R.id.volumes)

        rootPathView.setText(prefs.rootPath)
        portView.setText(prefs.port.toString())
        portView.inputType = InputType.TYPE_CLASS_NUMBER
        autostartView.isChecked = prefs.autostart

        rescanVolumes()
        findViewById<Button>(R.id.rescan).setOnClickListener {
            rescanVolumes()
            toast("rescanned storage")
        }
        volumesView.onItemSelectedListener = null

        findViewById<Button>(R.id.start).setOnClickListener {
            saveSettings()
            if (storageGrantOk()) {
                val i = Intent(this, ServerService::class.java)
                    .setAction(ServerService.ACTION_START)
                startService(i)
            }
        }
        findViewById<Button>(R.id.stop).setOnClickListener {
            startService(
                Intent(this, ServerService::class.java).setAction(ServerService.ACTION_STOP)
            )
        }
        autostartView.setOnCheckedChangeListener { _, on -> prefs.setAutostart(on) }

        findViewById<Button>(R.id.battery).setOnClickListener { askBatteryExemption() }
        findViewById<Button>(R.id.allfiles).setOnClickListener { askAllFilesAccess() }
        // "All files access" is an API 30 concept; on older devices the
        // classic permission is requested at Start instead.
        if (Build.VERSION.SDK_INT < 30) {
            findViewById<Button>(R.id.allfiles).visibility = android.view.View.GONE
        }
    }

    override fun onResume() {
        super.onResume()
        rescanVolumes()
        refresh.run()
    }

    override fun onPause() {
        super.onPause()
        Handler(Looper.getMainLooper()).removeCallbacks(refresh)
    }

    // ---------- storage picker ----------

    private fun rescanVolumes() {
        volumeEntries = StorageVolumes.list(this)
        val labels = volumeEntries.map { it.label }.toList()
        volumesView.adapter = ArrayAdapter(
            this, android.R.layout.simple_spinner_dropdown_item, labels
        )
        // Keep the text field in sync only when the user picks from the list.
        volumesView.onItemSelectedListener = object :
            android.widget.AdapterView.OnItemSelectedListener {
            override fun onItemSelected(
                parent: android.widget.AdapterView<*>?, view: android.view.View?,
                position: Int, id: Long
            ) {
                val sel = volumeEntries.getOrNull(position) ?: return
                // Convention: share a subfolder of the volume, never the volume
                // root itself, so a stray Android album dir never becomes "the share".
                val current = rootPathView.text.toString()
                if (current.isBlank() || current.startsWith(sel.path)) {
                    rootPathView.setText(sel.path + "/gvn")
                }
            }

            override fun onNothingSelected(parent: android.widget.AdapterView<*>?) {}
        }
        findViewById<TextView>(R.id.volumes_hint).text =
            "SD cards and USB sticks appear after you plug them in — press Rescan. " +
                "USB sticks need a phone that supplies OTG power (or a powered hub)."
    }

    // ---------- grants ----------

    /** Returns true when the Go process will be able to read/write the root. */
    private fun storageGrantOk(): Boolean {
        return when {
            Build.VERSION.SDK_INT >= 30 -> {
                if (!Environment.isExternalStorageManager()) {
                    toast("grant All-files access first")
                    askAllFilesAccess()
                    false
                } else {
                    maybeAskNotifications()
                    true
                }
            }
            Build.VERSION.SDK_INT >= 23 -> {
                if (checkSelfPermission(android.Manifest.permission.WRITE_EXTERNAL_STORAGE)
                    != PackageManager.PERMISSION_GRANTED
                ) {
                    requestPermissions(
                        arrayOf(android.Manifest.permission.WRITE_EXTERNAL_STORAGE), 1
                    )
                    false
                } else {
                    maybeAskNotifications()
                    true
                }
            }
            else -> true // API <= 22: install-time permissions, nothing to do
        }
    }

    private fun maybeAskNotifications() {
        if (Build.VERSION.SDK_INT >= 33 &&
            checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS)
            != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 2)
        }
    }

    override fun onRequestPermissionsResult(
        requestCode: Int, permissions: Array<out String>, results: IntArray
    ) {
        if (requestCode == 1 && results.firstOrNull() == PackageManager.PERMISSION_GRANTED) {
            // re-tap Start now that storage access is in
            toast("storage access granted — press Start again")
        }
    }

    private fun askAllFilesAccess() {
        if (Build.VERSION.SDK_INT < 30) return
        try {
            startActivity(
                Intent(
                    Settings.ACTION_MANAGE_APP_ALL_FILES_ACCESS_PERMISSION,
                    Uri.parse("package:$packageName")
                )
            )
        } catch (e: Exception) {
            startActivity(Intent(Settings.ACTION_MANAGE_ALL_FILES_ACCESS_PERMISSION))
        }
    }

    private fun askBatteryExemption() {
        try {
            startActivity(
                Intent(
                    Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
                    Uri.parse("package:$packageName")
                )
            )
        } catch (e: Exception) {
            startActivity(Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS))
        }
    }

    // ---------- settings + status ----------

    private fun saveSettings() {
        val root = rootPathView.text.toString().trim()
        if (root.isNotEmpty()) prefs.setRoot(root)
        val port = portView.text.toString().toIntOrNull()
        if (port != null && port in 1..65535) prefs.setPort(port)
    }

    private fun renderState() {
        val root = rootPathView.text.toString()
        if (ServerService.isRunning()) {
            val port = portView.text.toString().toIntOrNull() ?: prefs.port
            val urls = ServerService.localAddresses().joinToString("\n  ") {
                "https://$it:$port  (self-signed cert)"
            }
            statusView.text = "RUNNING — share: $root\n  ${urls.ifEmpty { "no LAN address found" } }"
        } else {
            statusView.text = "server stopped — share: $root"
        }
        logView.text = ServerService.logTail(25)
    }

    private fun toast(msg: String) = Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
}
