package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// citation 表示正文中一段文字引用的一个网页来源，Start 与 End 为字符下标，Offset 为 Text 的字节下标
type citation struct {
	URL    string
	Title  string
	Text   string
	Offset int
	Start  int
	End    int
}

// groundingCitations 把搜索来源对应到正文区间，片段按出现顺序在正文中查找，不在正文中的片段跳过
func groundingCitations(text string, grounding *antigravity.Grounding) []citation {
	var citations []citation
	cursor := 0
	for _, support := range grounding.GroundingSupports {
		index := strings.Index(text[cursor:], support.Segment.Text)
		if support.Segment.Text == "" || index < 0 {
			continue
		}
		start := cursor + index
		end := start + len(support.Segment.Text)
		cursor = end
		for _, index := range support.GroundingChunkIndices {
			if index >= len(grounding.GroundingChunks) || grounding.GroundingChunks[index].Web == nil {
				continue
			}
			web := grounding.GroundingChunks[index].Web
			citations = append(citations, citation{URL: web.URI, Title: web.Title, Text: text[start:end], Offset: start, Start: utf8.RuneCountInString(text[:start]), End: utf8.RuneCountInString(text[:end])})
		}
	}
	return citations
}

// groundingSources 返回搜索来源中的网页
func groundingSources(grounding *antigravity.Grounding) []antigravity.GroundingWeb {
	var sources []antigravity.GroundingWeb
	for _, chunk := range grounding.GroundingChunks {
		if chunk.Web != nil {
			sources = append(sources, *chunk.Web)
		}
	}
	return sources
}

// chatAnnotations 返回 Chat 消息的 url_citation 注释
func chatAnnotations(text string, grounding *antigravity.Grounding) []any {
	annotations := []any{}
	for _, item := range groundingCitations(text, grounding) {
		annotations = append(annotations, map[string]any{"type": "url_citation", "url_citation": map[string]any{
			"url": item.URL, "title": item.Title, "start_index": item.Start, "end_index": item.End,
		}})
	}
	return annotations
}

// responsesAnnotations 返回 Responses output_text 的 url_citation 注释
func responsesAnnotations(text string, grounding *antigravity.Grounding) []any {
	annotations := []any{}
	for _, item := range groundingCitations(text, grounding) {
		annotations = append(annotations, map[string]any{"type": "url_citation", "url": item.URL, "title": item.Title, "start_index": item.Start, "end_index": item.End})
	}
	return annotations
}

// interactionAnnotations 返回 Interactions text 内容的 url_citation 注释，下标按字节计
func interactionAnnotations(text string, grounding *antigravity.Grounding) []map[string]any {
	var annotations []map[string]any
	for _, item := range groundingCitations(text, grounding) {
		annotations = append(annotations, map[string]any{"type": "url_citation", "url": item.URL, "title": item.Title, "start_index": item.Offset, "end_index": item.Offset + len(item.Text)})
	}
	return annotations
}

// holdUntilGrounding 返回声明搜索时的写出函数与补发函数：首个正文起暂存事件，直到搜索来源或结束事件到达，先写出搜索来源再补发暂存的事件，使搜索结果排在正文之前；生成中途失败时由补发函数写出暂存的事件
func holdUntilGrounding(live func(antigravity.Event) error) (func(antigravity.Event) error, func() error) {
	var held []antigravity.Event
	holding, released := false, false
	release := func() error {
		released = true
		for _, previous := range held {
			if err := live(previous); err != nil {
				return err
			}
		}
		held = nil
		return nil
	}
	emit := func(event antigravity.Event) error {
		switch {
		case released:
			return live(event)
		case event.Kind == antigravity.EventGrounding || event.Kind == antigravity.EventFinish:
			if event.Kind == antigravity.EventGrounding {
				if err := live(event); err != nil {
					return err
				}
			}
			if err := release(); err != nil {
				return err
			}
			if event.Kind == antigravity.EventFinish {
				return live(event)
			}
			return nil
		case holding || event.Kind == antigravity.EventText:
			holding = true
			held = append(held, event)
			return nil
		}
		return live(event)
	}
	return emit, release
}

// searchOptions 转换搜索工具的上下文大小、用户位置与来源站点，都没有时返回 nil
func searchOptions(size string, location json.RawMessage, allowed, blocked []string) (*antigravity.SearchOptions, error) {
	switch size {
	case "", "low", "medium", "high":
	default:
		return nil, fmt.Errorf("search_context_size must be low, medium or high")
	}
	options := &antigravity.SearchOptions{ContextSize: size, AllowedDomains: allowed, BlockedDomains: blocked}
	if rawJSONConfigured(location) {
		type place struct {
			City     string `json:"city"`
			Region   string `json:"region"`
			Country  string `json:"country"`
			Timezone string `json:"timezone"`
		}
		var value struct {
			place
			Approximate *place `json:"approximate"`
		}
		if err := json.Unmarshal(location, &value); err != nil {
			return nil, fmt.Errorf("user_location must be an object")
		}
		if value.Approximate != nil {
			value.place = *value.Approximate
		}
		var parts []string
		for _, part := range []string{value.City, value.Region, value.Country} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		options.Location = strings.Join(parts, ", ")
		if value.Timezone != "" {
			options.Location = strings.TrimPrefix(options.Location+"; time zone "+value.Timezone, "; ")
		}
	}
	if options.ContextSize == "" && options.Location == "" && len(allowed) == 0 && len(blocked) == 0 {
		return nil, nil
	}
	return options, nil
}

// searchOnly 判断请求声明了 Google 搜索且没有函数，此时上游输出搜索来源
func searchOnly(tools antigravity.Tools) bool {
	return len(tools.GoogleSearch) > 0 && len(tools.Functions) == 0
}

// anthropicSearchBlocks 返回搜索来源对应的 server_tool_use 与 web_search_tool_result 块，查询取首个搜索查询
func anthropicSearchBlocks(id string, grounding *antigravity.Grounding) []anthropicContentBlock {
	query := ""
	if len(grounding.WebSearchQueries) > 0 {
		query = grounding.WebSearchQueries[0]
	}
	input, _ := json.Marshal(map[string]string{"query": query})
	results := []any{}
	for _, source := range groundingSources(grounding) {
		results = append(results, map[string]any{"type": "web_search_result", "url": source.URI, "title": source.Title, "encrypted_content": "", "page_age": nil})
	}
	content, _ := json.Marshal(results)
	return []anthropicContentBlock{
		{Type: "server_tool_use", ID: id, Name: "web_search", Input: input},
		{Type: "web_search_tool_result", ToolUseID: id, Content: content},
	}
}

// anthropicCitations 返回 text 块中引用搜索来源的 web_search_result_location
func anthropicCitations(text string, grounding *antigravity.Grounding) []any {
	var citations []any
	for _, item := range groundingCitations(text, grounding) {
		citations = append(citations, map[string]any{"type": "web_search_result_location", "url": item.URL, "title": item.Title, "encrypted_index": "", "cited_text": item.Text})
	}
	return citations
}

// anthropicSearchUsage 返回 usage 的 server_tool_use，搜索次数为查询数，网页读取次数为 0
func anthropicSearchUsage(grounding *antigravity.Grounding) map[string]any {
	return map[string]any{"web_fetch_requests": 0, "web_search_requests": max(1, len(grounding.WebSearchQueries))}
}
