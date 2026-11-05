package deepseek

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnirouter/internal/provider"
)

func TestDiscoverModelsMapsOfficialMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" { t.Fatalf("path = %s", r.URL.Path) }
		if r.Header.Get("Authorization") != "Bearer test-secret" { t.Fatalf("missing bearer authorization") }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"deepseek-flash","object":"model","owned_by":"deepseek","name":"DeepSeek V4.1 Flash","context_window":1048576,"max_output_tokens":393216,"input_modalities":["text","image"],"output_modalities":["text"],"effort":{"supported_levels":["low","high","max"],"default_level":"high"},"api_capabilities":{"anthropic_messages":{"system_prompt_update":"in-history"},"openai_responses":{}}},{"id":"deepseek-v4-pro","object":"model","owned_by":"deepseek","name":"DeepSeek V4 Pro","context_window":1048576,"max_output_tokens":393216,"input_modalities":["text"],"output_modalities":["text"],"effort":{"supported_levels":["low","high","max"]},"api_capabilities":{"anthropic_messages":{"system_prompt_update":"leading-only"},"openai_responses":{}}}]}`))
	}))
	defer server.Close()

	p, err := New(Config{ID:"deepseek", APIKey:"test-secret", BaseURL:server.URL, Client:server.Client()})
	if err != nil { t.Fatal(err) }
	models, err := p.DiscoverModels(context.Background())
	if err != nil { t.Fatal(err) }
	if len(models) != 2 { t.Fatalf("models = %d, want 2",len(models)) }
	flash := models[0]
	if flash.ID != "deepseek-flash" || flash.Name != "DeepSeek V4.1 Flash" || flash.ContextLimit != 1048576 {
		t.Fatalf("unexpected Flash metadata: %#v",flash)
	}
	for _, capability := range []string{"chat","streaming","tools","vision","reasoning","anthropic_messages","responses"} {
		if !hasCapability(flash,capability) { t.Errorf("Flash missing capability %q: %#v",capability,flash.Capabilities) }
	}
	pro := models[1]
	if hasCapability(pro,"vision") { t.Fatalf("text-only Pro unexpectedly declares vision: %#v",pro.Capabilities) }
	if !hasCapability(pro,"reasoning") || !hasCapability(pro,"responses") { t.Fatalf("Pro metadata missing: %#v",pro.Capabilities) }
}

func TestListModelsFallsBackWhenCatalogueUnavailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w,"unauthorized",http.StatusUnauthorized) }))
	defer server.Close()
	p, err := New(Config{ID:"deepseek",BaseURL:server.URL,Client:server.Client()})
	if err != nil { t.Fatal(err) }
	models, err := p.ListModels(context.Background())
	if err != nil { t.Fatal(err) }
	if len(models) != 2 || models[0].ID != "deepseek-flash" { t.Fatalf("unexpected fallback models: %#v",models) }
	if err := p.HealthCheck(context.Background()); err == nil || !strings.Contains(err.Error(),"HTTP 401") {
		t.Fatalf("HealthCheck error = %v, want HTTP 401",err)
	}
}

func TestGeneratePreservesDeepSeekThinkingFields(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" { t.Fatalf("path = %s",r.URL.Path) }
		if r.Header.Get("Authorization") != "Bearer test-secret" { t.Fatalf("missing bearer authorization") }
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil { t.Fatal(err) }
		w.Header().Set("Content-Type","application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","choices":[{"message":{"role":"assistant","reasoning_content":"reasoning segment","content":"final answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30,"completion_tokens_details":{"reasoning_tokens":7}}}`))
	}))
	defer server.Close()
	p, err := New(Config{ID:"deepseek",APIKey:"test-secret",BaseURL:server.URL,Client:server.Client()})
	if err != nil { t.Fatal(err) }
	input := []byte(`{"model":"deepseek-flash","messages":[{"role":"user","content":"hello"}],"thinking":{"type":"enabled"},"reasoning_effort":"high"}`)
	response, err := p.Generate(context.Background(),&provider.Request{Model:"deepseek-flash",Input:input})
	if err != nil { t.Fatal(err) }
	if received["thinking"] == nil || received["reasoning_effort"] != "high" { t.Fatalf("DeepSeek request fields were not preserved: %#v",received) }
	if !strings.Contains(string(response.Output),`"reasoning_content":"reasoning segment"`) || !strings.Contains(string(response.Output),`"reasoning_tokens":7`) {
		t.Fatalf("DeepSeek response fields were not preserved: %s",response.Output)
	}
}

func TestStreamPreservesReasoningDeltaAndDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type","text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	p, err := New(Config{ID:"deepseek",BaseURL:server.URL,Client:server.Client()})
	if err != nil { t.Fatal(err) }
	var payloads []string
	var terminal bool
	err = p.Stream(context.Background(),&provider.Request{Model:"deepseek-flash",Input:[]byte(`{"model":"deepseek-flash","stream":true,"thinking":{"type":"enabled"}}`),Stream:true},func(event provider.Event) error {
		if event.Done { terminal = event.ExplicitDone; return nil }
		payloads = append(payloads,string(event.Data))
		return nil
	})
	if err != nil { t.Fatal(err) }
	if !terminal { t.Fatal("expected explicit [DONE] terminal event") }
	joined := strings.Join(payloads,"\n")
	if !strings.Contains(joined,"reasoning_content") || !strings.Contains(joined,"thinking") || !strings.Contains(joined,"answer") {
		t.Fatalf("reasoning/content deltas not preserved: %s",joined)
	}
}

func hasCapability(model provider.Model, wanted string) bool {
	for _, capability := range model.Capabilities { if capability == wanted { return true } }
	return false
}
