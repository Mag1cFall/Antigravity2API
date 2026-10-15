package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/prompt"
)

// complete 以请求正文为光标前的文字、Suffix 为光标后的文字发出编辑器版 Tab 补全请求，读完上游流后输出光标处插入的文字
func (s *Service) complete(ctx context.Context, request antigravity.GenerateRequest, model antigravity.CatalogModel, tier prompt.Tier) (<-chan antigravity.Event, error) {
	var prefix strings.Builder
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			prefix.WriteString(part.Text)
		}
	}
	return s.dispatch(ctx, request, model, conversation{}, nil, "tab", 0, tier, func(deadline context.Context, current *account, catalog antigravity.CatalogModel) (<-chan antigravity.Event, *antigravity.Exchange, error) {
		file := antigravity.NewTabFile(current.device, prefix.String(), request.Suffix)
		envelope, err := antigravity.BuildTabEnvelope(current.client.Project(), catalog, file, time.Now().In(current.device.Location()))
		if err != nil {
			return nil, nil, &Error{Status: http.StatusBadRequest, Message: err.Error()}
		}
		upstream, exchange, err := current.client.StreamGenerateContent(deadline, envelope)
		if err != nil {
			return nil, exchange, err
		}
		events := make(chan antigravity.Event)
		go func() {
			defer close(events)
			var text strings.Builder
			for event := range upstream {
				switch event.Kind {
				case antigravity.EventText:
					text.WriteString(event.Text)
					continue
				case antigravity.EventFinish:
					if insertion := antigravity.TabInsertion(file, text.String()); insertion != "" {
						events <- antigravity.Event{Kind: antigravity.EventText, Text: insertion, ProviderModel: catalog.ID}
					}
				}
				events <- event
			}
		}()
		return events, exchange, nil
	})
}
