package main

/*
#include <stdlib.h>
*/
import "C"

import (
    "context"
    "log/slog"
    "net/http"
    "os"
    "path/filepath"
    "sync"
    "sync/atomic"
    "time"
    "unsafe"

    "omnirouter/internal/app"
    "omnirouter/internal/config"
    "omnirouter/internal/provider/deepseekweb"
)

type runtimeState struct {
    cancel      context.CancelFunc
    done        chan error
    application *app.App
}

var startupStage atomic.Int32
var startupFailedAt atomic.Int32
var startupFailedAt atomic.Int32

var state struct {
    sync.Mutex
    runtime *runtimeState
}

//export OmnirouterStartupStage
func OmnirouterStartupStage() C.int { return C.int(startupStage.Load()) }

//export OmnirouterStartupFailedAt
func OmnirouterStartupFailedAt() C.int { return C.int(startupFailedAt.Load()) }

func startupFail(code int32) C.int {
    startupFailedAt.Store(startupStage.Load())
    startupStage.Store(-2)
    return C.int(code)
}

//export OmnirouterStartupFailedAt
func OmnirouterStartupFailedAt() C.int { return C.int(startupFailedAt.Load()) }

func startupFail(code int32) C.int {
    startupFailedAt.Store(startupStage.Load())
    startupStage.Store(-2)
    return C.int(code)
}

//export OmnirouterStart
func OmnirouterStart(dataDir, gatewayKey, encryptionKey *C.char) C.int {
    startupFailedAt.Store(0)
    startupStage.Store(3)
    if dataDir == nil || gatewayKey == nil || encryptionKey == nil { return startupFail(-10) }

    state.Lock()
    defer state.Unlock()
    if state.runtime != nil { startupStage.Store(8); return 0 }

    root := C.GoString(dataDir)
    if root == "" { return startupFail(-11) }
    if err := os.MkdirAll(root, 0700); err != nil { return startupFail(-12) }

    startupStage.Store(4)
    values := map[string]string{
        "OMNI_ADDR": "127.0.0.1:8765",
        "OMNI_DATABASE_PATH": filepath.Join(root, "omnirouter.db"),
        "OMNI_GATEWAY_API_KEY": C.GoString(gatewayKey),
        "OMNI_CREDENTIAL_ENCRYPTION_KEY": C.GoString(encryptionKey),
        "OMNI_PROVIDER_KIND": "deepseek-web",
        "OMNI_PROVIDER_ID": "deepseek-web",
        "OMNI_PROVIDER_NAME": "DeepSeek Web",
        "OMNI_PROVIDER_BASE_URL": "https://chat.deepseek.com",
        "OMNI_PROVIDER_MODELS": "deepseek-web",
    }
    for key, value := range values {
        if err := os.Setenv(key, value); err != nil { return startupFail(-13) }
    }

    startupStage.Store(5)
    cfg, err := config.Load()
    if err != nil { return startupFail(-20) }
    logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

    startupStage.Store(6)
    application, err := app.New(cfg, logger)
    if err != nil { return startupFail(-21) }

    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan error, 1)
    runtime := &runtimeState{cancel: cancel, done: done, application: application}
    state.runtime = runtime
    startupStage.Store(7)
    go func() { done <- application.Run(ctx) }()

    startupStage.Store(8)
    deadline := time.Now().Add(10 * time.Second)
    client := &http.Client{Timeout: 500 * time.Millisecond}
    for time.Now().Before(deadline) {
        select {
        case <-done:
            cancel()
            state.runtime = nil
            return startupFail(-22)
        default:
        }
        response, requestErr := client.Get("http://127.0.0.1:8765/healthz")
        if requestErr == nil {
            _ = response.Body.Close()
            if response.StatusCode >= 200 && response.StatusCode < 300 {
                startupStage.Store(9)
                return 0
            }
        }
        time.Sleep(100 * time.Millisecond)
    }

    cancel()
    select {
    case <-done:
    case <-time.After(6 * time.Second):
    }
    state.runtime = nil
    return startupFail(-23)
}

//export OmnirouterPair
func OmnirouterPair(token *C.char, tokenLen C.int, cookie *C.char, cookieLen C.int) C.int {
    if token == nil || cookie == nil || tokenLen <= 0 || tokenLen > 16384 || cookieLen <= 0 || cookieLen > 4096 {
        return -40
    }
    tokenBytes := C.GoBytes(unsafe.Pointer(token), tokenLen)
    cookieBytes := C.GoBytes(unsafe.Pointer(cookie), cookieLen)
    defer func() {
        for i := range tokenBytes { tokenBytes[i] = 0 }
        for i := range cookieBytes { cookieBytes[i] = 0 }
    }()

    state.Lock()
    defer state.Unlock()
    runtime := state.runtime
    if runtime == nil || runtime.application == nil {
        return -41
    }
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    _, err := runtime.application.AuthorizeDeepSeekWebAccount(
        ctx,
        "deepseek-web",
        "Android WebView",
        deepseekweb.AuthorizationInput{
            Token: tokenBytes,
            Cookies: []deepseekweb.BrowserCookie{{
                Name: "ds_session_id", Value: string(cookieBytes),
                Domain: "chat.deepseek.com", Path: "/", Secure: true,
            }},
        },
    )
    if err != nil { return -42 }
    return 0
}

//export OmnirouterStop
func OmnirouterStop() C.int {
    state.Lock()
    runtime := state.runtime
    if runtime == nil {
        state.Unlock()
        startupStage.Store(0)
        return 0
    }
    state.runtime = nil
    runtime.cancel()
    state.Unlock()

    select {
    case <-runtime.done:
        startupStage.Store(0)
        return 0
    case <-time.After(6 * time.Second):
        startupStage.Store(-30)
        return -30
    }
}

func main() {}
