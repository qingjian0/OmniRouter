package com.qingjian0.omnirouter

import android.app.Activity
import android.os.Bundle
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

class ChatTestActivity : Activity() {
    private lateinit var prompt: EditText
    private lateinit var output: TextView
    private lateinit var sendButton: Button
    private lateinit var streamButton: Button

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val root = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(18, 18, 18, 18)
        }
        root.addView(TextView(this).apply {
            text = "DeepSeek 网页会话测试\n模型别名：deepseek-web\n接口：http://127.0.0.1:8765/v1/chat/completions"
            textSize = 15f
        }, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT))

        prompt = EditText(this).apply {
            hint = "输入一条测试消息"
            minLines = 3
            maxLines = 6
            setSingleLine(false)
            inputType = android.text.InputType.TYPE_CLASS_TEXT or android.text.InputType.TYPE_TEXT_FLAG_MULTI_LINE or android.text.InputType.TYPE_TEXT_FLAG_CAP_SENTENCES
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

        output = TextView(this).apply {
            text = "等待测试。请先完成 DeepSeek 账号连接。"
            textSize = 15f
            setTextIsSelectable(true)
            setPadding(10, 12, 10, 12)
        }
        val scroll = ScrollView(this).apply { addView(output) }
        root.addView(scroll, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1f))
        setContentView(root)
    }

    private fun send(stream: Boolean) {
        val message = prompt.text?.toString()?.trim().orEmpty()
        if (message.isEmpty()) {
            output.text = "请先输入测试消息。"
            return
        }
        sendButton.isEnabled = false
        streamButton.isEnabled = false
        output.text = if (stream) "正在建立 SSE 流式请求…\n" else "正在发送普通请求…\n"

        Thread {
            val result = try {
                val key = AndroidSecretStore(this).loadOrCreate().gatewayKey
                requestCompletion(message, key, stream)
            } catch (error: Throwable) {
                "请求失败：${error.message ?: error.javaClass.simpleName}"
            }
            runOnUiThread {
                output.text = result
                sendButton.isEnabled = true
                streamButton.isEnabled = true
            }
        }.start()
    }

    private fun requestCompletion(message: String, apiKey: String, stream: Boolean): String {
        val connection = (URL("http://127.0.0.1:8765/v1/chat/completions").openConnection() as HttpURLConnection).apply {
            requestMethod = "POST"
            connectTimeout = 10_000
            readTimeout = 180_000
            doOutput = true
            setRequestProperty("Authorization", "Bearer $apiKey")
            setRequestProperty("Content-Type", "application/json; charset=utf-8")
            setRequestProperty("Accept", if (stream) "text/event-stream" else "application/json")
        }
        try {
            val body = JSONObject()
                .put("model", "deepseek-web")
                .put("stream", stream)
                .put("messages", JSONArray().put(JSONObject().put("role", "user").put("content", message)))
                .toString()
                .toByteArray(StandardCharsets.UTF_8)
            connection.outputStream.use { it.write(body) }

            val status = connection.responseCode
            if (status !in 200..299) {
                val errorText = connection.errorStream?.bufferedReader(StandardCharsets.UTF_8)?.use { it.readText() }.orEmpty()
                return "HTTP $status\n${errorText.ifBlank { "本地网关未返回错误详情。" }}"
            }
            return if (stream) readSSE(connection) else readJSON(connection)
        } finally {
            connection.disconnect()
        }
    }

    private fun readJSON(connection: HttpURLConnection): String {
        val raw = connection.inputStream.bufferedReader(StandardCharsets.UTF_8).use { it.readText() }
        val root = JSONObject(raw)
        val choice = root.optJSONArray("choices")?.optJSONObject(0)
            ?: return "请求已返回，但没有 choices 内容。\n$raw"
        val message = choice.optJSONObject("message") ?: JSONObject()
        val reasoning = message.optString("reasoning_content")
        val content = message.optString("content")
        return buildString {
            if (reasoning.isNotBlank()) append("思考过程：\n").append(reasoning).append("\n\n")
            append(content.ifBlank { "上游返回成功，但没有文本内容。" })
        }
    }

    private fun readSSE(connection: HttpURLConnection): String {
        val result = StringBuilder()
        BufferedReader(InputStreamReader(connection.inputStream, StandardCharsets.UTF_8)).use { reader ->
            while (true) {
                val line = reader.readLine() ?: break
                if (!line.startsWith("data:")) continue
                val data = line.substringAfter("data:").trim()
                if (data == "[DONE]") break
                try {
                    val choice = JSONObject(data).optJSONArray("choices")?.optJSONObject(0) ?: continue
                    val delta = choice.optJSONObject("delta") ?: continue
                    val reasoning = delta.optString("reasoning_content")
                    val content = delta.optString("content")
                    if (reasoning.isNotEmpty()) result.append(reasoning)
                    if (content.isNotEmpty()) result.append(content)
                    val snapshot = result.toString()
                    runOnUiThread { output.text = snapshot }
                } catch (_: Throwable) {
                    // Ignore non-JSON SSE comments/metadata; the server owns protocol validation.
                }
            }
        }
        return result.toString().ifBlank { "SSE 已结束，但没有收到文本增量。" }
    }
}
