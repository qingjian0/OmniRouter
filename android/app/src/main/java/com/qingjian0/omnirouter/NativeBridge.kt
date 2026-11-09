package com.qingjian0.omnirouter

internal object GatewayStartupStatus {
    @Volatile var phase: Int = 0
    @Volatile var resultCode: Int? = null
    @Volatile var detail: String = "尚未启动"

    fun reset() {
        phase = 1
        resultCode = null
        detail = "正在准备启动"
    }
}

internal object NativeBridge {
    init {
        System.loadLibrary("omnirouter")
        System.loadLibrary("omnirouter_jni")
    }
    external fun nativeStart(dataDir: String, gatewayKey: String, encryptionKey: String): Int
    external fun nativePair(token: ByteArray, sessionCookie: ByteArray): Int
    external fun nativeStop(): Int
    external fun nativeGetStartupStage(): Int
    external fun nativeGetStartupFailedAt(): Int
}
