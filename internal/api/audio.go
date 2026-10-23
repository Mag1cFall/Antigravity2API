package api

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

const (
	// translationInstruction 为把转写文本译为英文的系统指令
	translationInstruction = "Translate the user's transcript into English. Keep text that is already English unchanged. Output only the translation."
	// transcriptionInstruction 为文本模型转写音频时音频前的指令
	transcriptionInstruction = "Transcribe the speech in the attached audio. Reply with the transcript only."
)

// handleOpenAITranscription 把上传的音频交给转写模型，按 json 或 text 返回转写文本；未指定模型时取目录中首个转写模型
func (s *server) handleOpenAITranscription(w http.ResponseWriter, r *http.Request) {
	audio, format, ok := openAIAudioForm(w, r)
	if !ok {
		return
	}
	model, _, dedicated, err := s.audioModels(r.Context(), r.FormValue("model"))
	if err == nil {
		var text string
		text, err = s.transcribeAudio(r.Context(), headerSession(r), model, audio, r.FormValue("prompt"), dedicated)
		if err == nil {
			writeTranscription(w, format, text)
			return
		}
	}
	if shouldWriteRequestError(r, err) {
		writeOpenAIRequestError(w, err)
	}
}

// handleOpenAITranslation 先用转写模型转写音频，再用文本模型译为英文；model 为文本模型时由它翻译，省略或为转写模型时由目录中首个文本模型翻译
func (s *server) handleOpenAITranslation(w http.ResponseWriter, r *http.Request) {
	audio, format, ok := openAIAudioForm(w, r)
	if !ok {
		return
	}
	transcriber, translator, dedicated, err := s.audioModels(r.Context(), r.FormValue("model"))
	if err == nil {
		var text string
		if text, err = s.transcribeAudio(r.Context(), headerSession(r), transcriber, audio, "", dedicated); err == nil && strings.TrimSpace(text) != "" {
			text, err = s.translateTranscript(r.Context(), headerSession(r), translator, text, r.FormValue("prompt"))
		}
		if err == nil {
			writeTranscription(w, format, text)
			return
		}
	}
	if shouldWriteRequestError(r, err) {
		writeOpenAIRequestError(w, err)
	}
}

// openAIAudioForm 读取 multipart 的 file 段与 response_format，失败时写入 OpenAI 错误并返回 false
func openAIAudioForm(w http.ResponseWriter, r *http.Request) (antigravity.Blob, string, bool) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return antigravity.Blob{}, "", false
	}
	format := cmp.Or(strings.TrimSpace(r.FormValue("response_format")), "json")
	if format != "json" && format != "text" {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("response_format %q is not compatible with the transcription model: it returns text without timestamps; use json or text", format))
		return antigravity.Blob{}, "", false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file is required")
		return antigravity.Blob{}, "", false
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil || len(data) == 0 {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "file must not be empty")
		return antigravity.Blob{}, "", false
	}
	mimeType := cmp.Or(declaredMediaType(header.Header.Get("Content-Type")), detectMediaType(header.Filename, data))
	return antigravity.Blob{MIME: mimeType, Data: data}, format, true
}

// audioModels 返回转写模型、翻译模型与转写模型是否为目录中的转写模型：model 为空或为转写模型时由它转写、目录中首个文本模型翻译，其余模型直接转写并翻译
func (s *server) audioModels(ctx context.Context, model string) (string, string, bool, error) {
	models, err := s.service.Models(ctx)
	if err != nil {
		return "", "", false, err
	}
	transcriber := catalogModel(models, "transcription")
	var translator string
	for _, entry := range models {
		if !entry.Capabilities["transcription"] && !entry.Capabilities["image_generation"] {
			translator = entry.ID
			break
		}
	}
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if entry, ok := lookupPublicModel(models, model); ok && !entry.Capabilities["transcription"] {
		return entry.ID, entry.ID, false, nil
	}
	return cmp.Or(model, transcriber), translator, true, nil
}

// transcribeAudio 以单条 user 消息发送音频并返回正文，文本模型在音频前带转写指令，prompt 不为空时接在指令之后
func (s *server) transcribeAudio(ctx context.Context, session, model string, audio antigravity.Blob, prompt string, dedicated bool) (string, error) {
	var texts []string
	if !dedicated {
		texts = append(texts, transcriptionInstruction)
	}
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		texts = append(texts, prompt)
	}
	var parts []antigravity.Part
	if len(texts) > 0 {
		parts = append(parts, antigravity.Part{Text: strings.Join(texts, "\n\n")})
	}
	parts = append(parts, antigravity.Part{InlineData: &audio})
	return s.generateText(ctx, antigravity.GenerateRequest{
		ID: newID("transcription"), Model: model, Session: session,
		Contents: []antigravity.Content{{Role: antigravity.RoleUser, Parts: parts}},
	})
}

// translateTranscript 用文本模型把转写文本译为英文，prompt 为客户端给出的风格说明
func (s *server) translateTranscript(ctx context.Context, session, model, transcript, prompt string) (string, error) {
	system := translationInstruction
	if prompt = strings.TrimSpace(prompt); prompt != "" {
		system += "\n\nContext and style guidance: " + prompt
	}
	return s.generateText(ctx, antigravity.GenerateRequest{
		ID: newID("translation"), Model: model, Session: session, System: system,
		Contents: []antigravity.Content{{Role: antigravity.RoleUser, Parts: []antigravity.Part{{Text: transcript}}}},
	})
}

// generateText 完成一次生成并返回去掉首尾空白的正文
func (s *server) generateText(ctx context.Context, request antigravity.GenerateRequest) (string, error) {
	result, err := s.generateResult(ctx, request)
	return strings.TrimSpace(result.text.String()), err
}

// writeTranscription 按 json 或 text 写出转写文本
func writeTranscription(w http.ResponseWriter, format, text string) {
	if format == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, text)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"text": text})
}
