package com.qingjian0.omnirouter

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.os.Build
import android.os.IBinder

internal class OmniRouterService : Service() {
    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            stopGateway()
            stopSelf(startId)
            return START_NOT_STICKY
        }
        startForeground(NOTIFICATION_ID, buildNotification())
        return try {
            val secrets = AndroidSecretStore(this).loadOrCreate()
            val result = NativeBridge.nativeStart(filesDir.absolutePath, secrets.gatewayKey, secrets.credentialEncryptionKey)
            if (result != 0) {
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf(startId)
                START_NOT_STICKY
            } else START_STICKY
        } catch (_: Throwable) {
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf(startId)
            START_NOT_STICKY
        }
    }

    override fun onDestroy() {
        stopGateway()
        super.onDestroy()
    }

    private fun stopGateway() {
        try { NativeBridge.nativeStop() } catch (_: Throwable) { }
        stopForeground(STOP_FOREGROUND_REMOVE)
    }

    private fun buildNotification(): Notification {
        val channelId = "omnirouter_gateway"
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            getSystemService(NotificationManager::class.java).createNotificationChannel(
                NotificationChannel(channelId, "DeepSeek local gateway", NotificationManager.IMPORTANCE_LOW)
            )
        }
        val builder = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) Notification.Builder(this, channelId) else {
            @Suppress("DEPRECATION") Notification.Builder(this)
        }
        return builder.setContentTitle("OmniRouter DeepSeek")
            .setContentText("本机 DeepSeek 网关正在运行")
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setOngoing(true)
            .build()
    }

    companion object {
        const val ACTION_START = "com.qingjian0.omnirouter.START"
        const val ACTION_STOP = "com.qingjian0.omnirouter.STOP"
        private const val NOTIFICATION_ID = 8765
    }
}
