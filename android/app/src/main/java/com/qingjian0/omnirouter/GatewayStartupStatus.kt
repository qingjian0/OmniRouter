package com.qingjian0.omnirouter

/**
 * Process-wide startup state shared by the foreground service and status UI.
 *
 * The service is the sole writer of lifecycle transitions; volatile fields make
 * the latest values visible to the Activity polling thread without initializing
 * NativeBridge or loading native libraries.
 */
internal object GatewayStartupStatus {
    /** 0 = idle, 1..9 = startup stages, -1 = stopping, -2 = failed. */
    @Volatile
    var phase: Int = 0

    /** Native stage that failed, or 0 when no individual stage is known. */
    @Volatile
    var failedAt: Int = 0

    @Volatile
    var detail: String = "尚未启动"

    @Volatile
    var resultCode: Int? = null

    fun reset() {
        failedAt = 0
        resultCode = null
        detail = "正在准备启动本地网关"
        phase = 0
    }
}
