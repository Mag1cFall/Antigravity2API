package tokencount

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"regexp"
	"strings"
	"sync"

	_ "golang.org/x/image/webp"

	sentencepiece "github.com/eliben/go-sentencepiece"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

//go:embed gemma3.model.gz
var model []byte

// processor 在首次计数时解析内嵌的 Gemma 3 分词模型
var processor = sync.OnceValue(func() *sentencepiece.Processor {
	reader, err := gzip.NewReader(bytes.NewReader(model))
	if err != nil {
		panic(err)
	}
	processor, err := sentencepiece.NewProcessor(reader)
	if err != nil {
		panic(err)
	}
	return processor
})

// factors 为各模型族 promptTokenCount 与 Gemma 3 计数之比：Text 为系统指令与消息，Tools 为函数声明 JSON；Gemini 与 GPT-OSS 的 Text 按 token-count runner 只含文本的样本拟合，Tools 按官方现场 agent 请求拟合，Claude 按官方现场 19 条 agent 请求与 1 条探针取单一系数
var factors = map[string]struct{ Text, Tools float64 }{
	antigravity.ProviderGoogle:    {Text: 0.9985, Tools: 1.0437},
	antigravity.ProviderOpenAI:    {Text: 0.9532, Tools: 0.7956},
	antigravity.ProviderAnthropic: {Text: 1.5946, Tools: 1.5946},
}

const (
	// geminiImageTokens 为 Gemini 族每张图片计入 promptTokenCount 的 token 数
	geminiImageTokens = 1092
	// geminiPDFPage 为 Gemini 族 PDF 每页计入 promptTokenCount 的 token 数
	geminiPDFPage = 516
	// claudeImagePatch 为 Claude 族图片计数的像素块边长，每块 1 个 token
	claudeImagePatch = 28
	// claudeImageEdge 为 Claude 族图片计数前长边缩放到的上限
	claudeImageEdge = 2576
	// claudePDFPage 与 claudePDFFile 为 Claude 族 PDF 每页与每个文件计入 promptTokenCount 的 token 数
	claudePDFPage, claudePDFFile = 1546, 88
)

// pdfPage 匹配 PDF 中的页对象
var pdfPage = regexp.MustCompile(`/Type\s*/Page\b`)

// Text 返回文本按 Gemma 3 分词的 token 数
func Text(value string) int64 {
	return int64(len(processor().Encode(value)))
}

// Prompt 估算上游请求计入 promptTokenCount 的 token 数：系统指令与各 part 的文本、函数名、参数和输出按 Gemma 3 计数后乘以模型族的文本系数，各函数声明的 JSON 乘以工具系数，图片与 PDF 另按模型族计数，未知模型族系数为 1
func Prompt(provider string, request antigravity.WireRequest) int64 {
	var text, tools, media int64
	if request.SystemInstruction != nil {
		for _, part := range request.SystemInstruction.Parts {
			text += Text(part.Text)
		}
	}
	for _, tool := range request.Tools {
		for _, declaration := range tool.FunctionDeclarations {
			encoded, _ := json.Marshal(declaration)
			tools += Text(string(encoded))
		}
	}
	var count func(parts []antigravity.WirePart)
	count = func(parts []antigravity.WirePart) {
		for _, part := range parts {
			text += Text(part.Text)
			if part.InlineData != nil {
				media += Media(provider, *part.InlineData)
			}
			if call := part.FunctionCall; call != nil {
				text += Text(call.Name) + Text(string(call.Args))
			}
			if response := part.FunctionResponse; response != nil {
				text += Text(response.Name) + Text(response.Response.Output)
				count(response.Parts)
			}
		}
	}
	for _, content := range request.Contents {
		count(content.Parts)
	}
	factor := family(provider)
	return int64(float64(text)*factor.Text+float64(tools)*factor.Tools+0.5) + media
}

// Media 返回一个内联媒体计入 promptTokenCount 的 token 数：Gemini 族每张图片 geminiImageTokens、PDF 每页 geminiPDFPage；Claude 族图片长边缩到 claudeImageEdge 以内后每 28×28 像素块 1 个，PDF 每个文件 claudePDFFile 加每页 claudePDFPage；其余媒体与模型族不计，无法读取尺寸的图片不计
func Media(provider string, blob antigravity.WireBlob) int64 {
	pdf := blob.MIMEType == "application/pdf"
	pages := int64(max(1, len(pdfPage.FindAll(blob.Data, -1))))
	switch {
	case provider == antigravity.ProviderGoogle && strings.HasPrefix(blob.MIMEType, "image/"):
		return geminiImageTokens
	case provider == antigravity.ProviderGoogle && pdf:
		return pages * geminiPDFPage
	case provider == antigravity.ProviderAnthropic && strings.HasPrefix(blob.MIMEType, "image/"):
		config, _, err := image.DecodeConfig(bytes.NewReader(blob.Data))
		if err != nil {
			return 0
		}
		width, height := float64(config.Width), float64(config.Height)
		if long := max(width, height); long > claudeImageEdge {
			width, height = width*claudeImageEdge/long, height*claudeImageEdge/long
		}
		return int64(math.Ceil(width/claudeImagePatch) * math.Ceil(height/claudeImagePatch))
	case provider == antigravity.ProviderAnthropic && pdf:
		return claudePDFFile + pages*claudePDFPage
	}
	return 0
}

// Scale 把正文的 Gemma 3 计数换算为模型族的 token 数
func Scale(provider string, tokens int64) int64 {
	return int64(float64(tokens)*family(provider).Text + 0.5)
}

// family 返回模型族的系数，未知模型族为 1
func family(provider string) struct{ Text, Tools float64 } {
	factor, ok := factors[provider]
	if !ok {
		return struct{ Text, Tools float64 }{1, 1}
	}
	return factor
}
