package tokencount

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"

	_ "golang.org/x/image/webp"

	"github.com/Mag1cFall/tiktoken-go"
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

//go:embed cl100k_base.tiktoken.gz
var cl100k []byte

//go:embed o200k_base.tiktoken.gz
var o200k []byte

// useEmbedded 把 tiktoken 的词表来源设为内嵌词表
var useEmbedded = sync.OnceFunc(func() { tiktoken.SetBpeLoader(embeddedLoader{}) })

// embeddedEncoding 返回在首次调用时以内嵌词表载入 name 编码的函数
func embeddedEncoding(name string) func() *tiktoken.Tiktoken {
	return sync.OnceValue(func() *tiktoken.Tiktoken {
		useEmbedded()
		encoding, err := tiktoken.GetEncoding(name)
		if err != nil {
			panic(err)
		}
		return encoding
	})
}

// claudeEncoding 与 gptEncoding 为 Claude 族的 cl100k_base 与 GPT-OSS 族的 o200k_base 编码
var claudeEncoding, gptEncoding = embeddedEncoding(tiktoken.MODEL_CL100K_BASE), embeddedEncoding(tiktoken.MODEL_O200K_BASE)

// embeddedLoader 从内嵌的 gzip 词表读取 BPE 序号
type embeddedLoader struct{}

// LoadTiktokenBpe 解析每行一个 base64 token 与序号的词表，按词表地址的文件名取内嵌的 o200k_base 或 cl100k_base
func (embeddedLoader) LoadTiktokenBpe(file string) (map[string]int, error) {
	data := cl100k
	if path.Base(file) == "o200k_base.tiktoken" {
		data = o200k
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	ranks := make(map[string]int)
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		token, rank, _ := strings.Cut(scanner.Text(), " ")
		decoded, err := base64.StdEncoding.DecodeString(token)
		if err != nil {
			return nil, err
		}
		if ranks[string(decoded)], err = strconv.Atoi(rank); err != nil {
			return nil, err
		}
	}
	return ranks, scanner.Err()
}

// factors 为各模型族 promptTokenCount 与分词计数之比：Text 为系统指令与消息，Tools 为函数声明 JSON；Gemini 以 Gemma 3 分词，Claude 以 cl100k_base 分词，计数方法见 docs/protocol.md「非流式与计数」
var factors = map[string]struct{ Text, Tools float64 }{
	antigravity.ProviderGoogle:    {Text: 0.9985, Tools: 1.0295},
	antigravity.ProviderAnthropic: {Text: 1.5787, Tools: 1.624},
}

const (
	// geminiImageTokens 为 Gemini 族每张图片计入 promptTokenCount 的 token 数
	geminiImageTokens = 1092
	// geminiPDFPage 为 Gemini 族 PDF 每页计入 promptTokenCount 的 token 数
	geminiPDFPage = 516
	// geminiDeclaration 为 Gemini 族每个函数声明另计的 token 数
	geminiDeclaration = 8
	// geminiSignatureChars 与 geminiSignatureBase 为 Gemini 族由思考签名长度估算其思考 token 数的每 token 字符数与签名的固定字符数
	geminiSignatureChars, geminiSignatureBase = 5.5, 150
	// claudeImagePatch 为 Claude 族图片计数的像素块边长，每块 1 个 token
	claudeImagePatch = 28
	// claudeImageEdge 为 Claude 族图片计数前长边缩放到的上限
	claudeImageEdge = 2576
	// claudePDFPage 与 claudePDFFile 为 Claude 族 PDF 每页与每个文件计入 promptTokenCount 的 token 数
	claudePDFPage, claudePDFFile = 1546, 88
	// claudeToolUse 为 Claude 族请求带函数声明时另计的 token 数
	claudeToolUse = 296
	// claudeDeclaration 为 Claude 族每个函数声明另计的 token 数
	claudeDeclaration = 28
	// claudeCallPart 为 Claude 族每个函数调用与函数结果 part 另计的 token 数
	claudeCallPart = 26
)

// pdfPage 匹配 PDF 中的页对象
var pdfPage = regexp.MustCompile(`/Type\s*/Page\b`)

// Text 返回文本按模型族分词的 token 数：Claude 族为 cl100k_base，GPT-OSS 族为 o200k_base，其余为 Gemma 3
func Text(provider, value string) int64 {
	switch provider {
	case antigravity.ProviderAnthropic:
		return int64(len(claudeEncoding().EncodeOrdinary(value)))
	case antigravity.ProviderOpenAI:
		return int64(len(gptEncoding().EncodeOrdinary(value)))
	}
	return int64(len(processor().Encode(value)))
}

// Prompt 估算上游请求计入 promptTokenCount 的 token 数，各模型族的计数规则见 docs/protocol.md「非流式与计数」
func Prompt(provider string, request antigravity.WireRequest) int64 {
	if provider == antigravity.ProviderOpenAI {
		return harmonyTokens(harmonyPrompt(request))
	}
	var text, tools, media, declarations, calls, signatures int64
	if request.SystemInstruction != nil {
		for _, part := range request.SystemInstruction.Parts {
			text += Text(provider, part.Text)
		}
	}
	for _, tool := range request.Tools {
		for _, declaration := range tool.FunctionDeclarations {
			encoded, _ := json.Marshal(declaration)
			tools += Text(provider, string(encoded))
			declarations++
		}
	}
	var count func(parts []antigravity.WirePart)
	count = func(parts []antigravity.WirePart) {
		for _, part := range parts {
			text += Text(provider, part.Text)
			signatures += Signature(provider, part.ThoughtSignature)
			if part.InlineData != nil {
				media += Media(provider, *part.InlineData)
			}
			if call := part.FunctionCall; call != nil {
				calls++
				text += Text(provider, call.Name) + Text(provider, string(call.Args))
			}
			if response := part.FunctionResponse; response != nil {
				calls++
				text += Text(provider, response.Name) + Text(provider, response.Response.Output)
				count(response.Parts)
			}
		}
	}
	for _, content := range request.Contents {
		count(content.Parts)
	}
	factor := family(provider)
	total := int64(float64(text)*factor.Text+float64(tools)*factor.Tools+0.5) + media
	if provider == antigravity.ProviderGoogle {
		total += geminiDeclaration*declarations + signatures
	}
	if provider == antigravity.ProviderAnthropic {
		total += claudeCallPart * calls
		if declarations > 0 {
			total += claudeToolUse + claudeDeclaration*declarations
		}
	}
	return total
}

// Signature 返回 Gemini 族一个思考签名计入 promptTokenCount 的 token 数：上游把签名所代表的思考按其 thoughtsTokenCount 计入输入，此处由签名长度估算；其余模型族与空签名为 0
func Signature(provider, signature string) int64 {
	if provider != antigravity.ProviderGoogle || len(signature) <= geminiSignatureBase {
		return 0
	}
	return int64(float64(len(signature)-geminiSignatureBase)/geminiSignatureChars + 0.5)
}

// Media 返回一个内联媒体计入 promptTokenCount 的 token 数，各模型族的图片与 PDF 计数规则见 docs/protocol.md「非流式与计数」
// 不支持的媒体类型、模型族或无法读取尺寸的图片返回 0
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

// Scale 把正文按 Text 的分词计数换算为模型族的 token 数
func Scale(provider string, tokens int64) int64 {
	return int64(float64(tokens)*family(provider).Text + 0.5)
}

// family 返回模型族的系数，GPT-OSS 与未知模型族为 1
func family(provider string) struct{ Text, Tools float64 } {
	factor, ok := factors[provider]
	if !ok {
		return struct{ Text, Tools float64 }{1, 1}
	}
	return factor
}
