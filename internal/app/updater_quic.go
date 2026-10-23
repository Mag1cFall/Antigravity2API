package app

import (
	crand "crypto/rand"
	"encoding/binary"
	"math/rand/v2"
	"time"

	quic "github.com/refraction-networking/uquic"
	"github.com/refraction-networking/uquic/quicvarint"
	qtls "github.com/refraction-networking/utls"
	"github.com/refraction-networking/utls/dicttls"
)

// quicPacketSize 是 Chromium 客户端 Initial 数据报的 UDP 载荷长度
const quicPacketSize = 1250

// quicPacketNumberLengths 是 Chromium 前两个 Initial 的包号编码长度，之后沿用最后一项
var quicPacketNumberLengths = []quic.PacketNumberLen{1, 2}

// quicConfig 是 Chromium 152 HTTP/3 会话的 QUIC 参数：30 秒空闲超时、1250 字节包、不做路径 MTU 探测
var quicConfig = &quic.Config{
	MaxIdleTimeout:                 30 * time.Second,
	InitialPacketSize:              quicPacketSize,
	DisablePathMTUDiscovery:        true,
	EnableDatagrams:                true,
	MaxIncomingStreams:             100,
	MaxIncomingUniStreams:          103,
	InitialStreamReceiveWindow:     6291456,
	InitialConnectionReceiveWindow: 15728640,
}

// quicSpec 返回一次连接的 Chromium 152 QUIC Initial：ClientHello 扩展与传输参数每次连接重新打乱，GREASE 传输参数与 GREASE 版本每次重新生成
func quicSpec() *quic.QUICSpec {
	versions := []uint32{qtls.VERSION_1, greaseVersion()}
	if rand.IntN(2) == 0 {
		versions[0], versions[1] = versions[1], versions[0]
	}
	grease := make([]byte, rand.IntN(16))
	crand.Read(grease)
	parameters := qtls.TransportParameters{
		qtls.InitialMaxStreamDataBidiLocal(6291456),
		qtls.InitialMaxStreamsBidi(100),
		qtls.InitialMaxData(15728640),
		qtls.InitialMaxStreamsUni(103),
		&qtls.FakeQUICTransportParameter{Id: 0x3128, Val: []byte("ORIG")},
		&qtls.FakeQUICTransportParameter{Id: rand.Uint64N(1<<62-31)/31*31 + 27, Val: grease},
		qtls.InitialMaxStreamDataBidiRemote(6291456),
		qtls.MaxIdleTimeout(30000),
		&qtls.VersionInformation{ChoosenVersion: qtls.VERSION_1, AvailableVersions: versions},
		qtls.InitialSourceConnectionID([]byte{}),
		qtls.MaxUDPPayloadSize(1472),
		qtls.MaxDatagramFrameSize(65536),
		qtls.InitialMaxStreamDataUni(6291456),
	}
	rand.Shuffle(len(parameters), func(i, j int) { parameters[i], parameters[j] = parameters[j], parameters[i] })
	extensions := []qtls.TLSExtension{
		&qtls.ALPNExtension{AlpnProtocols: []string{"h3"}},
		&qtls.UtlsCompressCertExtension{Algorithms: []qtls.CertCompressionAlgo{qtls.CertCompressionBrotli}},
		&qtls.PSKKeyExchangeModesExtension{Modes: []uint8{qtls.PskModeDHE}},
		&qtls.GREASEEncryptedClientHelloExtension{
			CandidateCipherSuites: []qtls.HPKESymmetricCipherSuite{{KdfId: dicttls.HKDF_SHA256, AeadId: dicttls.AEAD_AES_128_GCM}},
			CandidatePayloadLens:  []uint16{128, 160, 192, 224},
		},
		&qtls.QUICTransportParametersExtension{TransportParameters: parameters},
		&qtls.ApplicationSettingsExtensionNew{SupportedProtocols: []string{"h3"}},
		&qtls.SupportedVersionsExtension{Versions: []uint16{qtls.VersionTLS13}},
		&qtls.KeyShareExtension{KeyShares: []qtls.KeyShare{{Group: qtls.X25519MLKEM768}, {Group: qtls.X25519}}},
		&qtls.SignatureAlgorithmsExtension{SupportedSignatureAlgorithms: []qtls.SignatureScheme{
			qtls.ECDSAWithP256AndSHA256, qtls.PSSWithSHA256, qtls.PKCS1WithSHA256, qtls.ECDSAWithP384AndSHA384,
			qtls.PSSWithSHA384, qtls.PKCS1WithSHA384, qtls.PSSWithSHA512, qtls.PKCS1WithSHA512, qtls.PKCS1WithSHA1,
		}},
		&qtls.SNIExtension{},
		&qtls.SupportedCurvesExtension{Curves: []qtls.CurveID{qtls.X25519MLKEM768, qtls.X25519, qtls.CurveP256, qtls.CurveP384}},
	}
	rand.Shuffle(len(extensions), func(i, j int) { extensions[i], extensions[j] = extensions[j], extensions[i] })
	return &quic.QUICSpec{
		InitialPacketSpec: quic.InitialPacketSpec{
			DestConnIDLength:        8,
			InitPacketNumber:        1,
			InitPacketNumberLengths: quicPacketNumberLengths,
			InitialPackets:          []quic.InitialPacketPlan{{PacketSize: quicPacketSize}},
			FrameBuilder:            chromeFlight{},
		},
		ClientHelloSpec: &qtls.ClientHelloSpec{
			TLSVersMin:         qtls.VersionTLS13,
			TLSVersMax:         qtls.VersionTLS13,
			CipherSuites:       []uint16{qtls.TLS_AES_128_GCM_SHA256, qtls.TLS_AES_256_GCM_SHA384, qtls.TLS_CHACHA20_POLY1305_SHA256},
			CompressionMethods: []uint8{0},
			Extensions:         extensions,
		},
	}
}

// greaseVersion 是 quiche CreateRandomVersionLabelForNegotiation 生成的 0x?a?a?a?a 版本号
func greaseVersion() uint32 {
	var raw [4]byte
	crand.Read(raw[:])
	return binary.BigEndian.Uint32(raw[:])&0xf0f0f0f0 | 0x0a0a0a0a
}

// chromeFlight 按 Chromium QuicPacketCreator::MultiPacketChaosProtect 与 QuicChaosProtector 布置整个 Initial 航班
type chromeFlight struct{}

// Build 把 cryptoData 作为一个 Initial 包按 QuicChaosProtector 打散
func (chromeFlight) Build(cryptoData []byte) ([]byte, error) {
	return chaosProtect(cryptoData, []chaosFrame{{kind: 0x06, length: len(cryptoData)}}, frameBudget(0)), nil
}

// BuildFlight 首包放 ClientHello 开头 55–86 字节与末尾，其余包依次放中间部分，每包再按 QuicChaosProtector 打散
func (chromeFlight) BuildFlight(cryptoData []byte, _ []quic.InitialDatagramBudget) ([][]byte, error) {
	total := len(cryptoData)
	overhead := cryptoFrameSize(total-1, total)
	capacity := frameBudget(0) - overhead
	packets := (total + overhead + capacity - 1) / capacity
	firstLength := 55 + rand.IntN(32)
	lastLength := (total+overhead+packets-1)/packets - overhead - firstLength
	payloads := [][]byte{chaosProtect(cryptoData, []chaosFrame{{kind: 0x06, length: firstLength}, {kind: 0x06, offset: total - lastLength, length: lastLength}}, frameBudget(0))}
	for offset, end := firstLength, total-lastLength; offset < end; {
		budget := frameBudget(len(payloads))
		length := min(end-offset, budget-cryptoFrameSize(offset, end-offset))
		payloads = append(payloads, chaosProtect(cryptoData, []chaosFrame{{kind: 0x06, offset: offset, length: length}}, budget))
		offset += length
	}
	return payloads, nil
}

// frameBudget 返回第 index 个 Initial 包可放的帧字节数：数据报长度减去长包头（8 字节 DCID、空 SCID 与 token、2 字节 Length、包号）与 16 字节 AEAD 标签
func frameBudget(index int) int {
	return quicPacketSize - 16 - 18 - int(quicPacketNumberLengths[min(index, len(quicPacketNumberLengths)-1)])
}

// cryptoFrameSize 是 Chromium GetMinCryptoFrameSize：帧类型、偏移与长度的 varint 字节数
func cryptoFrameSize(offset, length int) int {
	return 1 + quicvarint.Len(uint64(offset)) + quicvarint.Len(uint64(length))
}

// chaosFrame 是 chaos protection 布置的一帧：CRYPTO 片段、PING 或指定字节数的 PADDING
type chaosFrame struct {
	kind    byte
	offset  int
	length  int
	padding int
}

// chaosProtect 按 QuicChaosProtector 拆分 CRYPTO 帧、加 PING、散布 PADDING 并打乱帧序，返回恰好 budget 字节的帧载荷
func chaosProtect(cryptoData []byte, frames []chaosFrame, budget int) []byte {
	padding := budget
	low, high := frames[0].offset, 0
	for _, frame := range frames {
		padding -= cryptoFrameSize(frame.offset, frame.length) + frame.length
		low, high = min(low, frame.offset), max(high, frame.offset+frame.length)
	}
	maxOverhead := cryptoFrameSize(high, high-low)
	for range 2 + rand.IntN(9) {
		if padding < maxOverhead {
			break
		}
		frame := &frames[rand.IntN(len(frames))]
		if frame.length <= 1 {
			continue
		}
		old := cryptoFrameSize(frame.offset, frame.length)
		kept := 1 + rand.IntN(frame.length-1)
		split := chaosFrame{kind: 0x06, offset: frame.offset + kept, length: frame.length - kept}
		frame.length = kept
		padding -= cryptoFrameSize(split.offset, split.length) + cryptoFrameSize(frame.offset, frame.length) - old
		frames = append(frames, split)
	}
	if padding > 0 {
		for range min(2+rand.IntN(9), padding) {
			frames = append(frames, chaosFrame{kind: 0x01})
			padding--
		}
	}
	spread := make([]chaosFrame, 0, 2*len(frames)+1)
	for _, frame := range frames {
		if bytes := rand.IntN(padding + 1); bytes > 0 {
			spread = append(spread, chaosFrame{padding: bytes})
			padding -= bytes
		}
		spread = append(spread, frame)
	}
	if padding > 0 {
		spread = append(spread, chaosFrame{padding: padding})
	}
	for i := len(spread) - 1; i > 0; i-- {
		j := rand.IntN(i + 1)
		spread[i], spread[j] = spread[j], spread[i]
	}
	payload := make([]byte, 0, budget)
	for _, frame := range spread {
		switch frame.kind {
		case 0x06:
			payload = append(payload, 0x06)
			payload = quicvarint.Append(payload, uint64(frame.offset))
			payload = quicvarint.Append(payload, uint64(frame.length))
			payload = append(payload, cryptoData[frame.offset:frame.offset+frame.length]...)
		case 0x01:
			payload = append(payload, 0x01)
		default:
			payload = append(payload, make([]byte, frame.padding)...)
		}
	}
	return payload
}
