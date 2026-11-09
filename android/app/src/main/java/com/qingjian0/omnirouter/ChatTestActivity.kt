package com.qingjian0.omnirouter

import android.app.Activity
import android.os.Bundle
import android.text.InputType
import android.view.ViewGroup
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import org.json.JSONArray
import org.json.JSONObject
import java.io.BufferedReader
import java.io.InputStreamReader
import java.net.HttpURLConnection
import java.net.URL
import java.nio.charset.StandardCharsets
import java.util.UUID

private data class ChatTurn(val role: String, val content: String)
private data class ChatOutcome(val displayText: String, val assistantContent: String?, val complete: Boolean)

class ChatTestActivity : Activity() {
    private lateinit var baseUrl: EditText
    private lateinit var endpoint: EditText
    private lateinit var modelId: EditText
    private lateinit var modelName: EditText
    private lateinit var apiKey: EditText
    private lateinit var prompt: EditText
    private lateinit var temperature: EditText
    private lateinit var maxTokens: EditText
    private lateinit var timeoutSeconds: EditText
    private lateinit var output: TextView
    private lateinit var sendButton: Button
    private lateinit var streamButton: Button
    private lateinit var newSessionButton: Button
    private lateinit var conversationLabel: TextView
    private val history = mutableListOf<ChatTurn>()
    private var conversationId = newConversationId()
    private val prefs by lazy { getSharedPreferences("omnirouter_chat_test_config", MODE_PRIVATE) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(16, 14, 16, 14)
        }
        root.addView(TextView(this).apply {
            text = "API 测试工作台"
            textSize = 20f
        })
        root.addView(TextView(this).apply {
            text = "可自定义 API 地址、接口路径、模型 ID、显示名称和请求参数。模型显示名称只用于界面展示，实际请求发送模型 ID。"
            textSize = 12f
        })

        val configScroll = ScrollView(this)
        val config = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        baseUrl = field(config, "API Base URL", "http://127.0.0.1:8765/v1")
        endpoint = field(config, "API 路径", "/chat/completions")
        modelId = field(config, "模型 ID（实际发送）", "deepseek-web")
        modelName = field(config, "模型显示名称（仅展示）", "DeepSeek Web · 默认")
        apiKey = field(config, "API Key（留空时本机网关自动读取应用密钥）", "", InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD)
        temperature = field(config, "Temperature", "0.7", InputType.TYPE_CLASS_NUMBER or InputType.TYPE_NUMBER_FLAG_DECIMAL)
        maxTokens = field(config, "Max tokens", "2048", InputType.TYPE_CLASS_NUMBER)
        timeoutSeconds = field(config, "超时时间（秒）", "180", InputType.TYPE_CLASS_NUMBER)
        loadConfig()

        val saveRow = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        saveRow.addView(Button(this).apply {
            text = "保存配置"
            setOnClickListener { saveConfig(); output.text = "配置已保存（API Key 不写入普通配置文件）。" }
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        saveRow.addView(Button(this).apply {
            text = "恢复默认"
            setOnClickListener {
                baseUrl.setText("http://127.0.0.1:8765/v1")
                endpoint.setText("/chat/completions")
                modelId.setText("deepseek-web")
                modelName.setText("DeepSeek Web · 默认")
                apiKey.setText("")
                temperature.setText("0.7")
                maxTokens.setText("2048")
                timeoutSeconds.setText("180")
                saveConfig()
                output.text = "已恢复默认测试配置。"
            }
        }, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        config.addView(saveRow)
        configScroll.addView(config)
        root.addView(configScroll, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 3f))

        conversationLabel = TextView(this).apply { textSize = 11f }
        updateConversationLabel()
        root.addView(conversationLabel)
        prompt = EditText(this).apply {
            hint = "输入你自己的测试提示词"
            minLines = 2
            maxLines = 4
            setSingleLine(false)
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_MULTI_LINE
        }
        root.addView(prompt, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))

        val buttons = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
        sendButton = Button(this).apply {
            text = "普通请求"
            setOnClickListener { send(stream = false) }
        }
        streamButton = Button(this).apply {
            text = "流式请求"
            setOnClickListener { send(stream = true) }
        }
        buttons.addView(sendButton, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        buttons.addView(streamButton, LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f))
        root.addView(buttons)
        newSessionButton = Button(this).apply {
            text = "新建会话 / 清空上下文"
            setOnClickListener {
                history.clear()
                conversationId = newConversationId()
                updateConversationLabel()
                output.text = "已创建新会话。"
            }
        }
        root.addView(newSessionButton)
        output = TextView(this).apply {
            text = "等待测试。请检查 API 地址和模型 ID。"
            textSize = 14f
            setTextIsSelectable(true)
            setPadding(8, 10, 8, 10)
        }
        root.addView(ScrollView(this).apply { addView(output) }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 4f))
        setContentView(root)
    }

    private fun field(parent: LinearLayout, label: String, default: String, type: Int = InputType.TYPE_CLASS_TEXT): EditText {
        parent.addView(TextView(this).apply { text = label; textSize = 12f })
        return EditText(this).also {
            it.setText(default)
            it.textSize = 14f
            it.inputType = type
            it.maxLines = 2
            parent.addView(it, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))
        }
    }

    private fun loadConfig() {
        baseUrl.setText(prefs.getString("base_url", "http://127.0.0.1:8765/v1"))
        endpoint.setText(prefs.getString("endpoint", "/chat/completions"))
        modelId.setText(prefs.getString("model_id", "deepseek-web"))
        modelName.setText(prefs.getString("model_name", "DeepSeek Web · 默认"))
        temperature.setText(prefs.getString("temperature", "0.7"))
        maxTokens.setText(prefs.getString("max_tokens", "2048"))
        timeoutSeconds.setText(prefs.getString("timeout_seconds", "180"))
    }

    private fun saveConfig() {
        prefs.edit()
            .putString("base_url", baseUrl.text.toString().trim())
            .putString("endpoint", endpoint.text.toString().trim())
            .putString("model_id", modelId.text.toString().trim())
            .putString("model_name", modelName.text.toString().trim())
            .putString("temperature", temperature.text.toString().trim())
            .putString("max_tokens", maxTokens.text.toString().trim())
            .putString("timeout_seconds", timeoutSeconds.text.toString().trim())
            .apply()
    }

    private fun newConversationId(): String = "android-" + UUID.randomUUID().toString().replace("-", "")
    private fun updateConversationLabel() { conversationLabel.text = "模型：${modelName.text}  |  会话 ID：$conversationId" }

    private fun send(stream: Boolean) {
        val message = prompt.text?.toString()?.trim().orEmpty()
        val base = baseUrl.text.toString().trim().trimEnd('/')
        val route = endpoint.text.toString().trim().let { if (it.startsWith("/")) it else "/$it" }
        val model = modelId.text.toString().trim()
        val temp = temperature.text.toString().trim().toDoubleOrNull()
        val tokens = maxTokens.text.toString().trim().toIntOrNull()
        val timeout = timeoutSeconds.text.toString().trim().toIntOrNull()
        if (message.isEmpty() || base.isEmpty() || model.isEmpty() || temp == null || tokens == null || tokens < 1 || timeout == null || timeout !in 5..900) {
            output.text = "配置错误：请填写提示词、API Base URL、模型 ID，并输入有效的 Temperature 和 Max tokens。"
            return
        }
        val target = try { URL(base + route) } catch (e: Exception) {
            output.text = "API 地址格式错误：${e.message}"
            return
        }
        saveConfig()
        updateConversationLabel()
        sendButton.isEnabled = false
        streamButton.isEnabled = false
        newSessionButton.isEnabled = false
        output.text = "阶段 1/4：配置校验完成\n阶段 2/4：连接 ${target}\n"
        val requestHistory = history.toList() + ChatTurn("user", message)
        val requestConversationId = conversationId
        val configuredKey = apiKey.text.toString()
        Thread {
            val outcome = try {
                val local = target.host == "127.0.0.1" || target.host == "localhost" || target.host == "::1"
                val key = configuredKey.ifBlank {
                    if (local) AndroidSecretStore(this).loadOrCreate().gatewayKey else ""
                }
                requestCompletion(target, model, requestHistory, requestConversationId, key, stream, temp, tokens, timeout)
            } catch (error: Throwable) {
                ChatOutcome("请求失败：${error.message ?: error.javaClass.simpleName}", null, false)
            }
            if (outcome.complete && outcome.assistantContent != null) {
                history.clear()
                history.addAll(requestHistory)
                history.add(ChatTurn("assistant", outcome.assistantContent))
            }
            runOnUiThread {
                output.text = outcome.displayText
                sendButton.isEnabled = true
                streamButton.isEnabled = true
                newSessionButton.isEnabled = true
            }
        }.start()
    }

    private fun requestCompletion(
        target: URL, model: String, messages: List<ChatTurn>, conversationId: String,
        apiKey: String, stream: Boolean, temperature: Double, maxTokens: Int, timeoutSeconds: Int
    ): ChatOutcome {
        val connection = (target.openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            connectTimeout = timeoutSeconds * 1000
            readTimeout = timeoutSeconds * 1000
            doOutput = true
            if (apiKey.isNotBlank()) setRequestProperty("Authorization", "Bearer $apiKey")
            setRequestProperty("X-OmniRouter-Conversation-ID", conversationId)
            setRequestProperty("Content-Type", "application/json; charset=utf-8")
            setRequestProperty("Accept", if (stream) "text/event-stream" else "application/json")
        }
        try {
            val messageArray = JSONArray()
            messages.forEach { turn -> messageArray.put(JSONObject().put("role", turn.role).put("content", turn.content)) }
            val body = JSONObject()
                .put("model", model)
                .put("stream", stream)
                .put("temperature", temperature)
                .put("max_tokens", maxTokens)
                .put("messages", messageArray)
                .toString()
                .toByteArray(StandardCharsets.UTF_8)
            runOnUiThread { output.text = "阶段 1/4：配置已保存\n阶段 2/4：发送请求（模型 ID：$model）\n" }
            connection.outputStream.use { it.write(body) }
            val status = connection.responseCode
            if (status !in 200..299) {
                val errorText = connection.errorStream?.bufferedReader(StandardCharsets.UTF_8)?.use { it.readText() }.orEmpty()
                return ChatOutcome("阶段 3/4：HTTP 响应异常\nHTTP 状态码：$status\n请求地址：$target\n错误详情：${errorText.ifBlank { "服务器未返回错误正文" }}", null, false)
            }
            runOnUiThread { output.text = "阶段 1/4：配置已保存\n阶段 2/4：HTTP $status，等待解析响应\n阶段 3/4：正在读取响应\n" }
            return if (stream) readSSE(connection, status, target) else readJSON(connection, status, target)
        } finally {
            connection.disconnect()
        }
    }

    private fun readJSON(connection: HttpURLConnection, status: Int, target: URL): ChatOutcome {
        val raw = connection.inputStream.bufferedReader(StandardCharsets.UTF_8).use { it.readText() }
        val root = JSONObject(raw)
        val choice = root.optJSONArray("choices")?.optJSONObject(0)
            ?: return ChatOutcome("HTTP $status\n响应缺少 choices：\n$raw", null, false)
        val message = choice.optJSONObject("message") ?: JSONObject()
        val reasoning = message.optString("reasoning_content")
        val content = message.optString("content")
        val display = buildString {
            append("阶段 1/4：配置校验成功\n阶段 2/4：HTTP $status\n请求地址：$target\n")
            append("模型 ID：${modelId.text}\n阶段 3/4：响应解析成功\n阶段 4/4：${if (content.isNotBlank()) "测试成功" else "响应无文本"}\n\n")
            if (reasoning.isNotBlank()) append("思考过程：\n").append(reasoning).append("\n\n")
            append(content.ifBlank { "上游返回成功，但没有文本内容。" })
        }
        return ChatOutcome(display, content, content.isNotBlank())
    }

    private fun readSSE(connection: HttpURLConnection, status: Int, target: URL): ChatOutcome {
        val reasoning = StringBuilder()
        val content = StringBuilder()
        var sawDone = false
        BufferedReader(InputStreamReader(connection.inputStream, StandardCharsets.UTF_8)).use { reader ->
            while (true) {
                val line = reader.readLine() ?: break
                if (!line.startsWith("data:")) continue
                val data = line.substringAfter("data:").trim()
                if (data == "[DONE]") { sawDone = true; break }
                try {
                    val choice = JSONObject(data).optJSONArray("choices")?.optJSONObject(0) ?: continue
                    val delta = choice.optJSONObject("delta") ?: continue
                    val reasoningDelta = delta.optString("reasoning_content")
                    val contentDelta = delta.optString("content")
                    if (reasoningDelta.isNotEmpty()) reasoning.append(reasoningDelta)
                    if (contentDelta.isNotEmpty()) content.append(contentDelta)
                    val snapshot = "HTTP $status · SSE 接收中\n请求地址：$target\n" +
                        (if (reasoning.isNotEmpty()) "思考过程：\n$reasoning\n\n" else "") + content
                    runOnUiThread { output.text = snapshot }
                } catch (_: Throwable) { /* Ignore SSE comments and non-JSON metadata. */ }
            }
        }
        val answer = content.toString()
        val display = buildString {
            append("阶段 1/4：配置校验成功\n阶段 2/4：HTTP $status\n请求地址：$target\n")
            append("阶段 3/4：SSE 读取结束\n阶段 4/4：${if (sawDone && answer.isNotBlank()) "测试成功" else "测试未完成"}\n\n")
            if (reasoning.isNotEmpty()) append("思考过程：\n").append(reasoning).append("\n\n")
            append(answer.ifBlank { "没有收到文本增量。" })
            append(if (sawDone) "\n\n[SSE DONE]" else "\n\n[警告] 未收到 [DONE] 终止标记。")
        }
        return ChatOutcome(display, answer, sawDone && answer.isNotBlank())
    }
}
