package sigstoreharness

import (
	"crypto/sha256"
	"fmt"
)

// leafHash is the RFC 6962 §2.1 leaf hash, SHA-256(0x00 || data).
func leafHash(data []byte) []byte {
	sum := sha256.Sum256(append([]byte{0x00}, data...))
	return sum[:]
}

// nodeHash is the RFC 6962 §2.1 interior hash, SHA-256(0x01 || l || r).
func nodeHash(l, r []byte) []byte {
	buf := make([]byte, 0, 1+len(l)+len(r))
	buf = append(buf, 0x01)
	buf = append(buf, l...)
	buf = append(buf, r...)
	sum := sha256.Sum256(buf)
	return sum[:]
}

// splitPoint returns the largest power of two smaller than n, for n > 1.
func splitPoint(n int) int {
	k := 1
	for k<<1 < n {
		k <<= 1
	}
	return k
}

// treeHead is the RFC 6962 §2.1 Merkle tree hash over leaf hashes.
func treeHead(leaves [][]byte) []byte {
	if len(leaves) == 1 {
		return leaves[0]
	}
	k := splitPoint(len(leaves))
	return nodeHash(treeHead(leaves[:k]), treeHead(leaves[k:]))
}

// auditPath is the RFC 6962 §2.1.1 inclusion path for leaf m, ordered
// from the leaf towards the root.
func auditPath(m int, leaves [][]byte) [][]byte {
	if len(leaves) <= 1 {
		return nil
	}
	k := splitPoint(len(leaves))
	if m < k {
		return append(auditPath(m, leaves[:k]), treeHead(leaves[k:]))
	}
	return append(auditPath(m-k, leaves[k:]), treeHead(leaves[:k]))
}

// inclusion is an entry's position in a generated log with its proof.
type inclusion struct {
	index      int
	size       int
	root       []byte
	hashes     [][]byte
	checkpoint string
}

// prove places body at index of a size-leaf tree whose other leaves are
// filler, and returns the audit path and the root.
func prove(body []byte, size, index int) (inclusion, error) {
	if size < 1 || index < 0 || index >= size {
		return inclusion{}, fmt.Errorf("tree position %d of %d is out of range", index, size)
	}
	leaves := make([][]byte, size)
	for i := range leaves {
		leaves[i] = leafHash(fmt.Appendf(nil, "acme filler entry %d", i))
	}
	leaves[index] = leafHash(body)
	return inclusion{
		index:  index,
		size:   size,
		root:   treeHead(leaves),
		hashes: auditPath(index, leaves),
	}, nil
}
