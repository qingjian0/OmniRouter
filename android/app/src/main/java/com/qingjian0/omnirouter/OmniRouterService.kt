package com.qingjian0.omnirouter

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.os.Build
import android.os.Handler
import android.os.IBinder
import android.os.Looper
import java.util.concurrent.Executors

internal class OmniRouterService : Service() {
    private val worker = Executors.newSingleThreadExecutor()
    private val mainHandler = Handler(Looper.getMainLooper())

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == ACTION_STOP) {
            GatewayStartupStatus.detail = "正在停止本地网关"
            GatewayStartupStatus.phase = -1
            worker.execute {
                val result = try { NativeBridge.nativeStop() } catch (_: Throwable) { -30 }
                mainHandler.post {
                    GatewayStartupStatus.resultCode = result
                    GatewayStartupStatus.phase = if (result == 0) 0 else -2
                    GatewayStartupStatus.detail = if (result == 0) "服务已停止" else "停止失败，错误码：$result"
                    stopForeground(STOP_FOREGROUND_REMOVE)
                    stopSelf(startId)
                }
            }
            return START_NOT_STICKY
        }

        startForeground(NOTIFICATION_ID, buildNotification("正在初始化…"))
        GatewayStartupStatus.reset()
        worker.execute {
            try {
                GatewayStartupStatus.phase = 1
                GatewayStartupStatus.detail = "检查 Android Keystore 与本地密钥"
                val secrets = AndroidSecretStore(this).loadOrCreate()

                GatewayStartupStatus.phase = 2
                GatewayStartupStatus.detail = "加载 ARM64 原生运行库"
                // Touch NativeBridge on this worker so library loading failures are captured here.
                val result = NativeBridge.nativeStart(filesDir.absolutePath, secrets.gatewayKey, secrets.credentialEncryptionKey)
                GatewayStartupStatus.resultCode = result
                if (result == 0) {
                    GatewayStartupStatus.phase = 8
                    GatewayStartupStatus.detail = "健康检查通过：127.0.0.1:8765/healthz"
                    mainHandler.post { updateNotification("DeepSeek 本地网关运行正常") }
                } else {
                    GatewayStartupStatus.phase = -2
                    GatewayStartupStatus.detail = "启动失败：${startupError(result)}（错误码 $result）"
                    mainHandler.post {
                        updateNotification("启动失败：${startupError(result)}")
                        stopForeground(STOP_FOREGROUND_REMOVE)
                        stopSelf(startId)
                    }
                }
            } catch (error: Throwable) {
                GatewayStartupStatus.phase = -2
                GatewayStartupStatus.detail = "启动异常：${error.javaClass.simpleName}: ${error.message ?: "无详细信息"}"
                mainHandler.post {
                    updateNotification("网关启动异常，请查看状态详情")
                    stopForeground(STOP_FOREGROUND_REMOVE)
                    stopSelf(startId)
                }
            }
        }
        return START_STICKY
    }

    private fun startupError(code: Int): String = when (code) {
        -10 -> "JNI 参数或原生库初始化失败"
        -11 -> "数据目录为空"
        -12 -> "无法创建应用数据目录"
        -13 -> "设置运行环境变量失败"
        -20 -> "配置加载失败"
        -21 -> "后端应用/数据库初始化失败"
        -22 -> "HTTP 服务启动后立即退出"
        -23 -> "等待本地健康检查超时"
        else -> "原生启动过程返回错误"
    }

    override fun onDestroy() {
        worker.execute {
            try { NativeBridge.nativeStop() } catch (_: Throwable) { }
        }
        worker.shutdown()
        super.onDestroy()
    }

    private fun buildNotification(message: String): Notification {
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
            .setContentText(message)
            .setSmallIcon(android.R.drawable.stat_notify_sync)
            .setOngoing(GatewayStartupStatus.phase !in setOf(0, -2))
            .build()
    }

    private fun updateNotification(message: String) {
        val manager = getSystemService(NotificationManager::class.java)
        manager.notify(NOTIFICATION_ID, buildNotification(message))
    }

    companion object {
        const val ACTION_START = "com.qingjian0.omnirouter.START"
        const val ACTION_STOP = "com.qingjian0.omnirouter.STOP"
        private const val NOTIFICATION_ID = 8765
    }
}
