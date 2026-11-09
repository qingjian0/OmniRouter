package com.qingjian0.omnirouter

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.ViewGroup
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import org.json.JSONObject

class MainActivity : Activity() {
    private lateinit var status: TextView
    private lateinit var detail: TextView
    private lateinit var stepsView: TextView
    private lateinit var webView: WebView
    private val statusHandler = Handler(Looper.getMainLooper())
    private val statusPoll = object : Runnable {
        override fun run() {
            renderGatewayStatus()
            statusHandler.postDelayed(this, 400)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        requestNotificationPermission()

        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(16, 16, 16, 16)
        }
        status = TextView(this).apply {
            text = "DeepSeek 专用本地网关\n默认地址：http://127.0.0.1:8765"
            textSize = 16f
        }
        root.addView(status, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        detail = TextView(this).apply {
            text = "启动状态：尚未启动"
            textSize = 13f
            setPadding(0, 8, 0, 12)
            setTextIsSelectable(true)
        }
        root.addView(detail, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        stepsView = TextView(this).apply { textSize = 12f; setTextIsSelectable(true); setPadding(0, 0, 0, 12) }
        root.addView(stepsView, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))

        val controls = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val serviceRow = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        serviceRow.addView(Button(this).apply {
            text = "启动服务"
            setOnClickListener {
                GatewayStartupStatus.reset()
                val serviceIntent = Intent(this@MainActivity, OmniRouterService::class.java).setAction(OmniRouterService.ACTION_START)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) startForegroundService(serviceIntent) else startService(serviceIntent)
                renderGatewayStatus()
            }
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        serviceRow.addView(Button(this).apply {
            text = "停止服务"
            setOnClickListener {
                startService(Intent(this@MainActivity, OmniRouterService::class.java).setAction(OmniRouterService.ACTION_STOP))
                GatewayStartupStatus.phase = -1
                GatewayStartupStatus.detail = "正在停止本地网关"
                renderGatewayStatus()
            }
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        controls.addView(serviceRow)

        val actionRow = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        actionRow.addView(Button(this).apply {
            text = "打开管理页"
            setOnClickListener { webView.loadUrl("http://127.0.0.1:8765/?android=1") }
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        actionRow.addView(Button(this).apply {
            text = "登录并连接 DeepSeek"
            setOnClickListener {
                val serviceIntent = Intent(this@MainActivity, OmniRouterService::class.java).setAction(OmniRouterService.ACTION_START)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) startForegroundService(serviceIntent) else startService(serviceIntent)
                startActivity(Intent(this@MainActivity, DeepSeekLoginActivity::class.java))
            }
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        controls.addView(actionRow)

        val testRow = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        testRow.addView(Button(this).apply {
            text = "测试对话（普通 / SSE）"
            setOnClickListener {
                val serviceIntent = Intent(this@MainActivity, OmniRouterService::class.java).setAction(OmniRouterService.ACTION_START)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) startForegroundService(serviceIntent) else startService(serviceIntent)
                startActivity(Intent(this@MainActivity, ChatTestActivity::class.java))
            }
        }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        controls.addView(testRow)
        root.addView(controls, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))

        webView = WebView(this).apply {
            settings.javaScriptEnabled = true
            settings.domStorageEnabled = true
            settings.allowFileAccess = false
            settings.allowContentAccess = false
            settings.mixedContentMode = android.webkit.WebSettings.MIXED_CONTENT_NEVER_ALLOW
            settings.safeBrowsingEnabled = true
            webChromeClient = WebChromeClient()
            webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
                    val uri = request.url
                    return !(uri.scheme == "http" && uri.host == "127.0.0.1" && uri.port == 8765)
                }

                override fun onPageFinished(view: WebView, url: String) {
                    if (url.startsWith("http://127.0.0.1:8765/")) injectGatewayKey(view)
                }
            }
        }
        root.addView(webView, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        setContentView(root)
        statusHandler.post(statusPoll)
    }

    private fun renderGatewayStatus() {
        val nativeStage = try { NativeBridge.nativeGetStartupStage() } catch (_: Throwable) { 0 }
        val nativeFailedAt = try { NativeBridge.nativeGetStartupFailedAt() } catch (_: Throwable) { 0 }
        val localPhase = GatewayStartupStatus.phase
        val current = if (localPhase == -1) -1 else if (nativeStage > 0) nativeStage else localPhase
        val failed = current == -2
        val stopping = current == -1
        val failedAt = if (nativeFailedAt > 0) nativeFailedAt else GatewayStartupStatus.failedAt
        val labels = listOf(
            "1. Android Keystore：读取/创建本地密钥",
            "2. ARM64 原生库：加载 Go 与 JNI 运行库",
            "3. 数据目录：检查并创建应用私有目录",
            "4. 运行配置：写入 DeepSeek-only 环境配置",
            "5. 配置解析：读取并校验网关配置",
            "6. 后端初始化：初始化应用与 SQLite 数据库",
            "7. HTTP 服务：启动 127.0.0.1:8765 监听",
            "8. 健康检查：请求 /healthz 并验证 HTTP 状态",
            "9. 就绪：本地网关可接受请求"
        )
        val lines = labels.mapIndexed { index, label ->
            val step = index + 1
            val state = when {
                failed && step == failedAt -> "异常"
                current == 0 && localPhase == 0 -> "待运行"
                failed && step < failedAt -> "已完成"
                failed -> "未执行"
                current == 0 -> "待运行"
                step < current -> "已完成"
                step == current -> if (step == 9) "成功" else "运行中"
                current == 9 -> "成功"
                else -> "等待中"
            }
            "$label  [$state]"
        }
        val success = current == 9 && !failed
        status.text = "DeepSeek 本地网关：${if (success) "运行正常" else if (failed) "启动失败" else if (stopping) "正在停止" else if (current == 0) "未启动" else "正在启动"}\n地址：http://127.0.0.1:8765"
        val error = GatewayStartupStatus.detail
        val activeStep = if (failed) failedAt else current
        val stageDetails = mapOf(
            1 to "读取或创建 Android Keystore 密钥",
            2 to "加载 libomnirouter.so 与 JNI 桥接库",
            3 to "检查并创建应用私有数据目录",
            4 to "设置 DeepSeek-only 运行环境变量",
            5 to "解析并校验运行配置",
            6 to "初始化后端应用、数据库及 Provider",
            7 to "启动 HTTP 服务并绑定 127.0.0.1:8765",
            8 to "轮询 /healthz，等待 HTTP 2xx（最多约 10 秒）",
            9 to "健康检查通过，网关已就绪"
        )
        val explanation = when {
            failed -> error
            activeStep in 3..9 -> stageDetails[activeStep] ?: error
            else -> error
        }
        detail.text = "当前阶段：${labels.getOrNull(activeStep - 1) ?: "等待服务状态"}\n结果：${if (success) "成功" else if (failed) "异常" else if (stopping) "停止中" else if (current == 0) "尚未启动" else "进行中"}\n阶段说明：$explanation\n错误详情：${if (failed) error else "无"}\n返回码：${GatewayStartupStatus.resultCode ?: "—"}"
        stepsView.text = lines.joinToString("\n")
    }

    private fun injectGatewayKey(view: WebView) {
        val key = try { AndroidSecretStore(this).loadOrCreate().gatewayKey } catch (_: Throwable) { return }
        val quoted = JSONObject.quote(key)
        val script = """(function(){
            const input = document.querySelector('input[aria-label="Gateway API Key"]');
            if (!input) return;
            const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
            setter.call(input, $quoted);
            input.dispatchEvent(new Event('input', { bubbles: true }));
            const button = Array.from(document.querySelectorAll('button')).find(b => (b.textContent || '').includes('连接 / 刷新'));
            if (button) button.click();
        })();"""
        view.evaluateJavascript(script, null)
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 8765)
        }
    }

    override fun onResume() {
        super.onResume()
        if (::status.isInitialized) statusHandler.post(statusPoll)
    }

    override fun onPause() {
        statusHandler.removeCallbacks(statusPoll)
        super.onPause()
    }

    override fun onDestroy() {
        statusHandler.removeCallbacks(statusPoll)
        webView.destroy()
        super.onDestroy()
    }
}
