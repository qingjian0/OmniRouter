package com.qingjian0.omnirouter

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
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
    private lateinit var webView: WebView

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

        val controls = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        val serviceRow = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        serviceRow.addView(Button(this).apply {
            text = "启动服务"
            setOnClickListener {
                val serviceIntent = Intent(this@MainActivity, OmniRouterService::class.java).setAction(OmniRouterService.ACTION_START)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) startForegroundService(serviceIntent) else startService(serviceIntent)
                status.text = "正在启动 DeepSeek 本地网关…"
            }
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        serviceRow.addView(Button(this).apply {
            text = "停止服务"
            setOnClickListener {
                startService(Intent(this@MainActivity, OmniRouterService::class.java).setAction(OmniRouterService.ACTION_STOP))
                status.text = "已请求停止服务"
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

    override fun onDestroy() {
        webView.destroy()
        super.onDestroy()
    }
}
