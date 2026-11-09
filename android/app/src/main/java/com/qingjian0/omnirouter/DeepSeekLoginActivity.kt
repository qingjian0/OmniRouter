package com.qingjian0.omnirouter

import android.app.Activity
import android.os.Bundle
import android.view.ViewGroup
import android.webkit.CookieManager
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import org.json.JSONTokener
import java.nio.charset.StandardCharsets

class DeepSeekLoginActivity : Activity() {
    private lateinit var status: TextView
    private lateinit var webView: WebView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        CookieManager.getInstance().setAcceptCookie(true)

        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(16, 16, 16, 16)
        }
        status = TextView(this).apply {
            text = "请在下方正常登录 DeepSeek。登录完成后，点击“连接此账号”。应用只读取当前站点所需的会话凭据，不会自动处理验证码或安全挑战。"
            textSize = 14f
        }
        root.addView(status, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))

        root.addView(Button(this).apply {
            text = "连接此账号"
            setOnClickListener { connectCurrentAccount() }
        })

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
                    return !(uri.scheme == "https" && uri.host == "chat.deepseek.com")
                }

                override fun onPageFinished(view: WebView, url: String) {
                    if (url.startsWith("https://chat.deepseek.com/")) {
                        status.text = "DeepSeek 页面已打开。请先完成正常登录，再点击上方“连接此账号”。"
                    }
                }
            }
            loadUrl("https://chat.deepseek.com/")
        }
        root.addView(webView, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        setContentView(root)
    }

    private fun connectCurrentAccount() {
        if (!webView.url.orEmpty().startsWith("https://chat.deepseek.com/")) {
            status.text = "当前页面不是 DeepSeek 官方登录域名，请重新打开登录页。"
            return
        }
        status.text = "正在检查当前 DeepSeek 登录状态…"
        val cookieHeader = CookieManager.getInstance().getCookie("https://chat.deepseek.com").orEmpty()
        val sessionCookie = cookieHeader.split(";")
            .map { it.trim() }
            .firstOrNull { it.startsWith("ds_session_id=") }
            ?.substringAfter("=", "")
            .orEmpty()
        if (sessionCookie.isBlank()) {
            status.text = "未找到 DeepSeek 会话 Cookie。请确认已在当前页面登录，然后重试。"
            return
        }

        webView.evaluateJavascript("JSON.stringify(window.localStorage.getItem('userToken') || '')") { encoded ->
            val token = try {
                JSONTokener(encoded).nextValue() as? String ?: ""
            } catch (_: Throwable) {
                ""
            }
            if (token.isBlank()) {
                status.text = "未找到当前页面的登录令牌。请刷新 DeepSeek 页面并确认登录状态。"
                return@evaluateJavascript
            }

            val tokenBytes = token.toByteArray(StandardCharsets.UTF_8)
            val cookieBytes = sessionCookie.toByteArray(StandardCharsets.UTF_8)
            status.text = "正在将授权凭据加密保存到本机…"
            Thread {
                val result = try {
                    NativeBridge.nativePair(tokenBytes, cookieBytes)
                } catch (_: Throwable) {
                    -99
                } finally {
                    tokenBytes.fill(0)
                    cookieBytes.fill(0)
                }
                runOnUiThread {
                    status.text = if (result == 0) {
                        "DeepSeek 登录态已加密保存在本机。尚未完成真实推理验收；请返回管理页发送一条测试请求。"
                    } else {
                        "连接失败（代码 $result）。请确认本地网关已启动、账号仍有效，然后重试。"
                    }
                }
            }.start()
        }
    }

    override fun onDestroy() {
        webView.destroy()
        super.onDestroy()
    }
}
