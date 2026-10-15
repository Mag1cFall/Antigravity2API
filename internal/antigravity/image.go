package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// requestTypeImage 为图像生成请求的 requestType
	requestTypeImage = "image_gen"
	// defaultAspectRatio 为官方 generate_image 工具未指定宽高比时的取值
	defaultAspectRatio = "1:1"
)

// ImageEnvelope 表示官方 generate_image 工具发出的 generateContent 请求体，没有 systemInstruction、labels 与 sessionId
type ImageEnvelope struct {
	Project     string       `json:"project"`
	RequestID   string       `json:"requestId"`
	Request     ImageRequest `json:"request"`
	Model       string       `json:"model"`
	UserAgent   string       `json:"userAgent"`
	RequestType string       `json:"requestType"`
}

// ImageRequest 表示图像生成信封内的请求
type ImageRequest struct {
	Contents         []WireContent         `json:"contents"`
	GenerationConfig ImageGenerationConfig `json:"generationConfig"`
}

// ImageGenerationConfig 表示图像生成的候选数与图像参数
type ImageGenerationConfig struct {
	CandidateCount int             `json:"candidateCount"`
	ImageConfig    WireImageConfig `json:"imageConfig"`
}

// WireImageConfig 表示上游图像参数
type WireImageConfig struct {
	AspectRatio string `json:"aspectRatio"`
	ImageSize   string `json:"imageSize,omitempty"`
}

// BuildImageEnvelope 按官方 generate_image 工具的请求组装图像生成信封：requestId 为 image_gen/<毫秒>/<trajectory_id>/<步序号>，步序号为 2N+2，客户端系统提示以空行接在首个 user 轮的文本之前，宽高比缺省为 1:1
func BuildImageEnvelope(project string, model CatalogModel, conversation Conversation, system string, contents []Content, config GenerationConfig, now time.Time) (ImageEnvelope, error) {
	wired, err := wireContents(contents, model)
	if err != nil {
		return ImageEnvelope{}, err
	}
	if system != "" {
		wired = withInstruction(wired, system)
	}
	image := WireImageConfig{AspectRatio: defaultAspectRatio}
	if config.ImageConfig != nil {
		if config.ImageConfig.AspectRatio != "" {
			image.AspectRatio = config.ImageConfig.AspectRatio
		}
		image.ImageSize = config.ImageConfig.ImageSize
	}
	return ImageEnvelope{
		Project:     project,
		RequestID:   fmt.Sprintf("%s/%d/%s/%d", requestTypeImage, now.UnixMilli(), conversation.TrajectoryID, 2*conversation.Turn+2),
		Request:     ImageRequest{Contents: wired, GenerationConfig: ImageGenerationConfig{CandidateCount: 1, ImageConfig: image}},
		Model:       model.ID,
		UserAgent:   envelopeUserAgent,
		RequestType: requestTypeImage,
	}, nil
}

// withInstruction 把指令并入首个 user 轮：首个 part 为文本时以空行接在其前，否则作为新的首个文本 part
func withInstruction(contents []WireContent, instruction string) []WireContent {
	for index, content := range contents {
		if content.Role != wireRoleUser {
			continue
		}
		parts := append([]WirePart(nil), content.Parts...)
		if len(parts) > 0 && parts[0].Text != "" {
			parts[0].Text = instruction + "\n\n" + parts[0].Text
		} else {
			parts = append([]WirePart{{Text: instruction}}, parts...)
		}
		cloned := append([]WireContent(nil), contents...)
		cloned[index].Parts = parts
		return cloned
	}
	return append([]WireContent{{Role: wireRoleUser, Parts: []WirePart{{Text: instruction}}}}, contents...)
}

// WriteTrajectoryACL 为新轨迹发出 writeTrajectoryAcls，与官方子代理开始时相同
func (c *Client) WriteTrajectoryACL(ctx context.Context, trajectory string) {
	c.post(ctx, c.base+"/v1internal:writeTrajectoryAcls", trajectoryACL{TrajectoryID: trajectory}, nil)
}

// GenerateContent 以 Content-Length 正文发送非流式 generateContent，把响应解码为与流式相同的规范事件，同时返回本次请求与响应的正文
func (c *Client) GenerateContent(ctx context.Context, envelope any) (<-chan Event, *Exchange, error) {
	payload, err := hubJSON(envelope)
	if err != nil {
		return nil, nil, err
	}
	exchange := &Exchange{URL: c.base + "/v1internal:generateContent", Request: payload}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, exchange.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, exchange, err
	}
	request.Header.Set("User-Agent", c.device.UserAgent())
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(c.traced(request))
	if err != nil {
		return nil, exchange, err
	}
	defer response.Body.Close()
	exchange.Response, err = io.ReadAll(response.Body)
	if err != nil {
		return nil, exchange, err
	}
	if response.StatusCode/100 != 2 {
		return nil, exchange, &UpstreamError{Status: response.StatusCode, Body: exchange.Response}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, exchange.Response); err != nil {
		return nil, exchange, err
	}
	events := make(chan Event)
	go func() {
		defer close(events)
		DecodeStream(io.MultiReader(bytes.NewReader([]byte("data: ")), &compact), events)
	}()
	return events, exchange, nil
}
