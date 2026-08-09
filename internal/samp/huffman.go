package samp

import (
	"errors"
	"github.com/SA-MP-Android/SA-MP-Pilot/internal/raknet"
)

var errCompressedString = errors.New("samp: invalid compressed string")
var englishFrequencies = [256]uint32{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 722, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 11084, 58, 63, 1, 0, 31, 0, 317, 64, 64, 44, 0, 695, 62, 980, 266, 69, 67, 56, 7, 73, 3, 14, 2, 69, 1, 167, 9, 1, 2, 25, 94, 0, 195, 139, 34, 96, 48, 103, 56, 125, 653, 21, 5, 23, 64, 85, 44, 34, 7, 92, 76, 147, 12, 14, 57, 15, 39, 15, 1, 1, 1, 2, 3, 0, 3611, 845, 1077, 1884, 5870, 841, 1057, 2501, 3212, 164, 531, 2019, 1330, 3056, 4037, 848, 47, 2586, 2919, 4771, 1707, 535, 1106, 152, 1243, 100, 0, 2, 0, 10}

type huffmanNode struct {
	weight      uint32
	value       byte
	left, right *huffmanNode
}

var englishTree = buildHuffmanTree()

func buildHuffmanTree() *huffmanNode {
	nodes := make([]*huffmanNode, 0, 256)
	insert := func(node *huffmanNode) {
		at := 0
		for at < len(nodes) && nodes[at].weight < node.weight {
			at++
		}
		nodes = append(nodes, nil)
		copy(nodes[at+1:], nodes[at:])
		nodes[at] = node
	}
	for value, weight := range englishFrequencies {
		if weight == 0 {
			weight = 1
		}
		insert(&huffmanNode{weight: weight, value: byte(value)})
	}
	for len(nodes) > 1 {
		left, right := nodes[0], nodes[1]
		nodes = nodes[2:]
		insert(&huffmanNode{weight: left.weight + right.weight, left: left, right: right})
	}
	return nodes[0]
}
func decodeHuffmanString(r *raknet.Reader, max int) ([]byte, error) {
	bitLength, e := r.CompressedUint16()
	if e != nil || int(bitLength) > r.Remaining() {
		return nil, errCompressedString
	}
	node := englishTree
	out := make([]byte, 0, min(max, int(bitLength)))
	for i := 0; i < int(bitLength); i++ {
		bit, e := r.Bit()
		if e != nil {
			return nil, e
		}
		if bit {
			node = node.right
		} else {
			node = node.left
		}
		if node == nil {
			return nil, errCompressedString
		}
		if node.left == nil && node.right == nil {
			if len(out) >= max {
				return nil, errCompressedString
			}
			out = append(out, node.value)
			node = englishTree
		}
	}
	return out, nil
}
