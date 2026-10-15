package api

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// imageToolName 为 Responses image_generation 工具声明给模型的函数名
const imageToolName = "image_generation"

// imageToolDeclaration 为 image_generation 工具声明给模型的函数
var imageToolDeclaration = antigravity.FunctionDeclaration{
	Name:        imageToolName,
	Description: "Generate an image from a detailed description, or edit the images the user attached. The image is shown to the user.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string","description":"Detailed description of the image to generate or of the edit to apply"}},"required":["prompt"]}`),
}

// hostedImages 在请求带 image_generation 工具时把模型对它的调用换成目录中图像生成模型输出的图片事件；首个调用起的事件暂存到文本生成结束、账户槽位归还后再请求图像模型并按原顺序输出
func (s *server) hostedImages(ctx context.Context, tools []responsesTool, contents []antigravity.Content, events <-chan antigravity.Event) <-chan antigravity.Event {
	index := slices.IndexFunc(tools, func(tool responsesTool) bool { return tool.Type == "image_generation" })
	if index < 0 {
		return events
	}
	out := make(chan antigravity.Event)
	go func() {
		defer close(out)
		send := func(items []antigravity.Event) bool {
			for _, item := range items {
				select {
				case out <- item:
				case <-ctx.Done():
					return false
				}
			}
			return true
		}
		var pending []antigravity.Event
		for event := range events {
			if len(pending) > 0 || event.Kind == antigravity.EventToolCall && event.ToolCall.Name == imageToolName {
				pending = append(pending, event)
			} else if !send([]antigravity.Event{event}) {
				return
			}
		}
		for _, event := range pending {
			replaced := []antigravity.Event{event}
			if event.Kind == antigravity.EventToolCall && event.ToolCall.Name == imageToolName {
				replaced = s.hostedImage(ctx, tools[index], contents, event.ToolCall.Arguments)
			}
			if !send(replaced) {
				return
			}
		}
	}()
	return out
}

// hostedImage 以调用参数中的提示词与最近一条 user 消息中的图片请求图像生成模型，返回不带签名的图片事件
func (s *server) hostedImage(ctx context.Context, tool responsesTool, contents []antigravity.Content, arguments json.RawMessage) []antigravity.Event {
	var call struct {
		Prompt string `json:"prompt"`
	}
	json.Unmarshal(arguments, &call)
	models, err := s.service.Models(ctx)
	if err != nil {
		return []antigravity.Event{{Kind: antigravity.EventError, Err: err}}
	}
	config, err := openAIImageConfig(tool.Size, tool.Quality)
	if err != nil {
		return []antigravity.Event{{Kind: antigravity.EventError, Err: fmt.Errorf("image_generation tool: %w", err)}}
	}
	parts := []antigravity.Part{{Text: call.Prompt}}
	for index := len(contents) - 1; index >= 0; index-- {
		if contents[index].Role != antigravity.RoleUser {
			continue
		}
		for _, part := range contents[index].Parts {
			if part.InlineData != nil && strings.HasPrefix(part.InlineData.MIME, "image/") {
				parts = append(parts, antigravity.Part{InlineData: part.InlineData})
			}
		}
		break
	}
	result, err := s.generateResult(ctx, antigravity.GenerateRequest{
		ID: newID("ig"), Model: catalogModel(models, "image_generation"),
		Contents: []antigravity.Content{{Role: antigravity.RoleUser, Parts: parts}},
		Config:   antigravity.GenerationConfig{ResponseModalities: []string{"IMAGE"}, ImageConfig: config},
	})
	if err == nil && len(result.media) == 0 {
		err = fmt.Errorf("the image model returned no image: finish reason %s", result.finishReason)
	}
	if err != nil {
		return []antigravity.Event{{Kind: antigravity.EventError, Err: err}}
	}
	events := make([]antigravity.Event, 0, len(result.media))
	for _, media := range result.media {
		events = append(events, antigravity.Event{Kind: antigravity.EventMedia, Media: &media})
	}
	return events
}

// generateResult 完成一次生成并返回汇总结果
func (s *server) generateResult(ctx context.Context, request antigravity.GenerateRequest) (generationResult, error) {
	events, err := s.service.Generate(ctx, request)
	if err != nil {
		return generationResult{}, err
	}
	return consumeEvents(ctx, events, nil)
}
