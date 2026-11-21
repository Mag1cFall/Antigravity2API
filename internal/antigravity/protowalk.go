package antigravity

import (
	"bytes"
	"encoding/binary"
	"errors"

	"google.golang.org/protobuf/encoding/protowire"
)

// pbNode 表示一个 protobuf 字段：wire 为线型，num 为字段号，u 为 varint 值，raw 为长度前缀或定长字段的原始字节
type pbNode struct {
	num  int
	wire int
	u    uint64
	raw  []byte
}

// pbParse 解析一段 protobuf 为字段序列，保持原有顺序
func pbParse(data []byte) ([]pbNode, error) {
	var out []pbNode
	for len(data) > 0 {
		field, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		data = data[n:]
		node := pbNode{num: int(field), wire: int(kind)}
		switch kind {
		case protowire.VarintType:
			node.u, n = protowire.ConsumeVarint(data)
		case protowire.BytesType:
			node.raw, n = protowire.ConsumeBytes(data)
		case protowire.Fixed64Type, protowire.Fixed32Type:
			n = protowire.ConsumeFieldValue(field, kind, data)
			if n >= 0 {
				node.raw = data[:n]
			}
		default:
			return nil, errors.New("unsupported protobuf wire type")
		}
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		data = data[n:]
		out = append(out, node)
	}
	return out, nil
}

// walkWire 解析消息并依次回调各字段：长度前缀字段给出内容，varint 字段给出数值
func walkWire(raw []byte, visit func(field int, value []byte, number uint64) error) error {
	nodes, err := pbParse(raw)
	if err != nil {
		return err
	}
	for _, node := range nodes {
		var value []byte
		var number uint64
		switch node.wire {
		case int(protowire.BytesType):
			value = node.raw
		case int(protowire.VarintType):
			number = node.u
		}
		if err := visit(node.num, value, number); err != nil {
			return err
		}
	}
	return nil
}

// pbEncode 把字段序列按原有顺序编码回字节
func pbEncode(nodes []pbNode) []byte {
	var out []byte
	for _, node := range nodes {
		out = binary.AppendUvarint(out, uint64(node.num<<3|node.wire))
		switch node.wire {
		case 0:
			out = binary.AppendUvarint(out, node.u)
		case 2:
			out = binary.AppendUvarint(out, uint64(len(node.raw)))
			out = append(out, node.raw...)
		default:
			out = append(out, node.raw...)
		}
	}
	return out
}

// pbWalk 对字段序列深度优先调用 fn，fn 先在每个字段上执行，随后只递归进可无损往返的子消息字段，字符串字段按叶子处理；返回重新编码后的字节
func pbWalk(data []byte, fn func(n *pbNode)) []byte {
	nodes, err := pbParse(data)
	if err != nil {
		return data
	}
	for i := range nodes {
		node := &nodes[i]
		fn(node)
		if node.wire == 2 && len(node.raw) > 0 && pbRoundTrips(node.raw) {
			node.raw = pbWalk(node.raw, fn)
		}
	}
	return pbEncode(nodes)
}

// pbRoundTrips 返回字节能否无损解析并重新编码为同一串，即它确实是一段子消息而非恰好可解析的字符串
func pbRoundTrips(data []byte) bool {
	sub, err := pbParse(data)
	return err == nil && bytes.Equal(pbEncode(sub), data)
}
