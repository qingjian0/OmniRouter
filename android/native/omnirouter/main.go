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
    "time"

    "omnirouter/internal/app"
    "omnirouter/internal/config"
)

type runtimeState struct {
    cancel context.CancelFunc
    done   chan error
}

var state struct {
    sync.Mutex
    runtime *runtimeState
}

//export OmnirouterStart
func OmnirouterStart(dataDir, gatewayKey, encryptionKey *C.char) C.int {
    if dataDir == nil || gatewayKey == nil || encryptionKey == nil { return -10 }

    state.Lock()
    defer state.Unlock()
    if state.runtime != nil { return 0 }

    root := C.GoString(dataDir)
    if root == "" { return -11 }
    if err := os.MkdirAll(root, 0700); err != nil { return -12 }

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
        if err := os.Setenv(key, value); err != nil { return -13 }
    }

    cfg, err := config.Load()
    if err != nil { return -20 }
    logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
    application, err := app.New(cfg, logger)
    if err != nil { return -21 }

    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan error, 1)
    runtime := &runtimeState{cancel: cancel, done: done}
    state.runtime = runtime
    go func() { done <- application.Run(ctx) }()

    deadline := time.Now().Add(10 * time.Second)
    client := &http.Client{Timeout: 500 * time.Millisecond}
    for time.Now().Before(deadline) {
        select {
        case <-done:
            cancel()
            state.runtime = nil
            return -22
        default:
        }
        response, requestErr := client.Get("http://127.0.0.1:8765/healthz")
        if requestErr == nil {
            _ = response.Body.Close()
            if response.StatusCode >= 200 && response.StatusCode < 300 { return 0 }
        }
        time.Sleep(100 * time.Millisecond)
    }

    cancel()
    select {
    case <-done:
    case <-time.After(6 * time.Second):
    }
    state.runtime = nil
    return -23
}

//export OmnirouterStop
func OmnirouterStop() C.int {
    state.Lock()
    runtime := state.runtime
    if runtime == nil {
        state.Unlock()
        return 0
    }
    state.runtime = nil
    runtime.cancel()
    state.Unlock()

    select {
    case <-runtime.done:
        return 0
    case <-time.After(6 * time.Second):
        return -30
    }
}

func main() {}
