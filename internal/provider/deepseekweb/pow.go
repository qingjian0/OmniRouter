package deepseekweb

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/tetratelabs/wazero"
)

// DeepSeek's current web client ships this WASM implementation of
// DeepSeekHashV1. Keep the module embedded so Android ARM64 does not need an
// external runtime download or an architecture-specific native library.
// The upstream challenge remains authoritative and is fetched for every turn.
type powChallengeEnvelope struct {
	Code int `json:"code"`
	Data struct {
		BizCode int `json:"biz_code"`
		BizData struct {
			Challenge struct {
				Algorithm  string          `json:"algorithm"`
				Challenge  string          `json:"challenge"`
				Salt       string          `json:"salt"`
				Difficulty float64         `json:"difficulty"`
				Signature  string          `json:"signature"`
				ExpireAt   json.RawMessage `json:"expire_at"`
				TargetPath string          `json:"target_path"`
			} `json:"challenge"`
		} `json:"biz_data"`
	} `json:"data"`
}

func (t *HTTPCompletionTransport) createPoWHeader(ctx context.Context, material AuthorizationMaterial, accessToken []byte) (string, error) {
	headers := t.headers(accessToken, material.CookieHeader)
	headers.Set("Content-Type", "application/json")
	headers.Set("Accept", "application/json")
	body, _ := json.Marshal(struct {
		TargetPath string `json:"target_path"`
	}{TargetPath: completionPath})
	response, err := t.client.Do(ctx, http.MethodPost, powChallengePath, headers, strings.NewReader(string(body)))
	if err != nil {
		return "", fmt.Errorf("request DeepSeek Web proof-of-work challenge: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("request DeepSeek Web proof-of-work challenge: %w", SafeFailure(response.StatusCode))
	}
	raw, err := websession.ReadBoundedBody(response.Body, maxJSONResponseBytes)
	if err != nil {
		return "", fmt.Errorf("read DeepSeek Web proof-of-work challenge: %w", err)
	}
	var envelope powChallengeEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", fmt.Errorf("%w: invalid proof-of-work challenge JSON", ErrUpstreamChanged)
	}
	challenge := envelope.Data.BizData.Challenge
	if envelope.Code != 0 || envelope.Data.BizCode != 0 ||
		challenge.Algorithm != "DeepSeekHashV1" ||
		challenge.Challenge == "" || challenge.Salt == "" ||
		challenge.Signature == "" || challenge.Difficulty <= 0 ||
		len(challenge.ExpireAt) == 0 {
		return "", fmt.Errorf("%w: incomplete or unsupported proof-of-work challenge", ErrUpstreamChanged)
	}
	if challenge.TargetPath != "" && challenge.TargetPath != completionPath {
		return "", fmt.Errorf("%w: proof-of-work target path mismatch", ErrUpstreamChanged)
	}
	expireAt, err := jsonScalarString(challenge.ExpireAt)
	if err != nil {
		return "", fmt.Errorf("%w: invalid proof-of-work expiry", ErrUpstreamChanged)
	}
	answer, err := solveDeepSeekHashV1(ctx, challenge.Challenge, challenge.Salt+"_"+expireAt+"_", challenge.Difficulty)
	if err != nil {
		return "", fmt.Errorf("solve DeepSeek Web proof-of-work challenge: %w", err)
	}
	targetPath := challenge.TargetPath
	if targetPath == "" {
		targetPath = completionPath
	}
	payload := struct {
		Algorithm  string `json:"algorithm"`
		Challenge  string `json:"challenge"`
		Salt       string `json:"salt"`
		Answer     int64  `json:"answer"`
		Signature  string `json:"signature"`
		TargetPath string `json:"target_path"`
	}{
		Algorithm: challenge.Algorithm, Challenge: challenge.Challenge,
		Salt: challenge.Salt, Answer: answer, Signature: challenge.Signature,
		TargetPath: targetPath,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode DeepSeek Web proof-of-work response: %w", err)
	}
	return base64.StdEncoding.EncodeToString(encoded), nil
}

func jsonScalarString(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		if text == "" { return "", fmt.Errorf("empty scalar") }
		return text, nil
	}
	var number json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err != nil || number.String() == "" {
		return "", fmt.Errorf("expected string or number")
	}
	return number.String(), nil
}

func solveDeepSeekHashV1(ctx context.Context, challenge, prefix string, difficulty float64) (int64, error) {
	if challenge == "" || prefix == "" || math.IsNaN(difficulty) || math.IsInf(difficulty, 0) || difficulty <= 0 {
		return 0, fmt.Errorf("invalid challenge parameters")
	}
	runtime := wazero.NewRuntime(ctx)
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, deepSeekPoWWASM)
	if err != nil { return 0, fmt.Errorf("compile embedded DeepSeek WASM: %w", err) }
	module, err := runtime.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil { return 0, fmt.Errorf("instantiate embedded DeepSeek WASM: %w", err) }
	defer module.Close(ctx)
	memory := module.Memory()
	malloc := module.ExportedFunction("__wbindgen_export_0")
	stack := module.ExportedFunction("__wbindgen_add_to_stack_pointer")
	solve := module.ExportedFunction("wasm_solve")
	if memory == nil || malloc == nil || stack == nil || solve == nil {
		return 0, fmt.Errorf("embedded DeepSeek WASM exports are incomplete")
	}
	stackResult, err := stack.Call(ctx, uint64(uint32(int32(-16))))
	if err != nil || len(stackResult) == 0 { return 0, fmt.Errorf("reserve WASM return slot: %w", err) }
	retptr := uint32(stackResult[0])
	defer stack.Call(ctx, uint64(16))
	writeString := func(value string) (uint32, uint32, error) {
		data := []byte(value)
		result, callErr := malloc.Call(ctx, uint64(len(data)), uint64(1))
		if callErr != nil || len(result) == 0 { return 0, 0, fmt.Errorf("allocate WASM input: %w", callErr) }
		ptr := uint32(result[0])
		if !memory.Write(ptr, data) { return 0, 0, fmt.Errorf("write WASM input out of bounds") }
		return ptr, uint32(len(data)), nil
	}
	challengePtr, challengeLen, err := writeString(challenge)
	if err != nil { return 0, 0, err }
	prefixPtr, prefixLen, err := writeString(prefix)
	if err != nil { return 0, 0, err }
	_, err = solve.Call(ctx, uint64(retptr), uint64(challengePtr), uint64(challengeLen),
		uint64(prefixPtr), uint64(prefixLen), math.Float64bits(difficulty))
	if err != nil { return 0, fmt.Errorf("execute DeepSeekHashV1 WASM: %w", err) }
	result, ok := memory.Read(retptr, 16)
	if !ok { return 0, fmt.Errorf("read WASM result out of bounds") }
	status := int32(binary.LittleEndian.Uint32(result[:4]))
	answer := math.Float64frombits(binary.LittleEndian.Uint64(result[8:16]))
	if status == 0 || math.IsNaN(answer) || math.IsInf(answer, 0) || answer < 0 || answer > math.MaxInt64 {
		return 0, fmt.Errorf("DeepSeekHashV1 returned no valid answer")
	}
	return int64(answer), nil
}

// Keep strconv linked as a guard against accidentally changing expiry
// normalization to a lossy float conversion in future protocol updates.
var _ = strconv.IntSize
