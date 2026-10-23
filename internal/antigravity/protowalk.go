package antigravity

import (
	"bytes"
	"encoding/binary"
)

// pbNode 表示一个 protobuf 字段：wire 为线型，num 为字段号，u 为 varint 值，raw 为长度前缀字段的原始字节
type pbNode struct {
	num  int
	wire int
	u    uint64
	raw  []byte
}

// pbParse 解析一段 protobuf 为字段序列，保持原有顺序；解析失败时返回 nil
func pbParse(data []byte) []pbNode {
	var out []pbNode
	for i := 0; i < len(data); {
		key, n := binary.Uvarint(data[i:])
		if n <= 0 {
			return nil
		}
		i += n
		node := pbNode{num: int(key >> 3), wire: int(key & 7)}
		switch node.wire {
		case 0:
			v, m := binary.Uvarint(data[i:])
			if m <= 0 {
				return nil
			}
			node.u, i = v, i+m
		case 1:
			if i+8 > len(data) {
				return nil
			}
			node.raw, i = data[i:i+8], i+8
		case 2:
			l, m := binary.Uvarint(data[i:])
			if m <= 0 || i+m+int(l) > len(data) {
				return nil
			}
			i += m
			node.raw, i = data[i:i+int(l)], i+int(l)
		case 5:
			if i+4 > len(data) {
				return nil
			}
			node.raw, i = data[i:i+4], i+4
		default:
			return nil
		}
		out = append(out, node)
	}
	return out
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
	nodes := pbParse(data)
	if nodes == nil {
		return data
	}
	for i := range nodes {
		node := &nodes[i]
		fn(node)
		if node.wire == 2 && len(node.raw) > 0 {
			if sub := pbParse(node.raw); sub != nil && bytes.Equal(pbEncode(sub), node.raw) {
				node.raw = pbWalk(node.raw, fn)
			}
		}
	}
	return pbEncode(nodes)
}

// pbRoundTrips 返回字节能否无损解析并重新编码为同一串，即它确实是一段子消息而非恰好可解析的字符串
func pbRoundTrips(data []byte) bool {
	sub := pbParse(data)
	return sub != nil && bytes.Equal(pbEncode(sub), data)
}
