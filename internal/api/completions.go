package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// completionRequest 为 OpenAI Completions 请求中服务使用的字段
type completionRequest struct {
	Model         string          `json:"model"`
	Prompt        json.RawMessage `json:"prompt"`
	Suffix        string          `json:"suffix"`
	Stream        bool            `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

// promptText 返回 prompt 的文字：字符串或只含一个字符串的数组
func (request completionRequest) promptText() (string, error) {
	var text string
	if json.Unmarshal(request.Prompt, &text) == nil {
		return text, nil
	}
	var texts []string
	if json.Unmarshal(request.Prompt, &texts) == nil && len(texts) == 1 {
		return texts[0], nil
	}
	return "", errors.New("prompt must be a string or an array with one string")
}

// handleCompletions 以 prompt 为光标前的文字、suffix 为光标后的文字生成补全，返回 text_completion 对象或流
func (s *server) handleCompletions(w http.ResponseWriter, r *http.Request) {
	var request completionRequest
	if err := decodeJSON(r, &request); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Model == "" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "model is required")
		return
	}
	prompt, err := request.promptText()
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	id := newID("cmpl")
	events, err := s.service.Generate(r.Context(), antigravity.GenerateRequest{
		ID:       id,
		Model:    request.Model,
		Contents: []antigravity.Content{{Role: antigravity.RoleUser, Parts: []antigravity.Part{{Text: prompt}}}},
		Suffix:   request.Suffix,
	})
	if err != nil {
		if shouldWriteRequestError(r, err) {
			writeOpenAIRequestError(w, err)
		}
		return
	}
	created := time.Now().Unix()
	chunk := func(text string, finish any) map[string]any {
		return map[string]any{"id": id, "object": "text_completion", "created": created, "model": request.Model,
			"choices": []any{map[string]any{"text": text, "index": 0, "logprobs": nil, "finish_reason": finish}}}
	}
	if !request.Stream {
		result, err := consumeEvents(r.Context(), events, nil)
		if err != nil {
			if shouldWriteRequestError(r, err) {
				writeOpenAIRequestError(w, err)
			}
			return
		}
		body := chunk(result.text.String(), openAIFinishReason(result.finishReason, false))
		if result.usage != nil {
			body["usage"] = openAIUsage(result.usage)
		}
		writeJSON(w, http.StatusOK, body)
		return
	}
	if err := streamHeaders(w); err != nil {
		return
	}
	result, err := consumeStreamEvents(r.Context(), events, func(event antigravity.Event) error {
		if event.Kind != antigravity.EventText {
			return nil
		}
		return writeSSE(w, "", chunk(event.Text, nil))
	}, func() error { return writeSSEHeartbeat(w) })
	if err != nil {
		if shouldWriteRequestError(r, err) {
			status := statusFromError(err)
			code := openAIErrorCode(err)
			_ = writeSSE(w, "", map[string]any{"error": map[string]any{"message": err.Error(), "type": openAIErrorType(status, code), "code": code}})
		}
		return
	}
	final := chunk("", openAIFinishReason(result.finishReason, false))
	if request.StreamOptions.IncludeUsage && result.usage != nil {
		final["usage"] = openAIUsage(result.usage)
	}
	if writeSSE(w, "", final) == nil {
		_ = writeSSEText(w, "[DONE]")
	}
}
