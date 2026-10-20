package dev.gvn.explorer

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build

class BootReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action != Intent.ACTION_BOOT_COMPLETED) return
        if (!Prefs(ctx).autostart) return
        val i = Intent(ctx, ServerService::class.java).setAction(ServerService.ACTION_START)
        if (Build.VERSION.SDK_INT >= 26) {
            ctx.startForegroundService(i)
        } else {
            ctx.startService(i)
        }
    }
}
