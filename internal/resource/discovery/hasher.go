package discovery

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func StableID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:16])
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type NodeHashes struct {
	NameHash     string
	PathHash     string
	ContentHash  string
	SnapshotHash string
}

// ComputeNodeHashes computes deterministic SHA-256 digests for name, path, content, and composite snapshot.
func ComputeNodeHashes(name, fullPath string, parentID int64, isDir bool, content string) NodeHashes {
	dirFlag := 0
	if isDir {
		dirFlag = 1
	}
	nameH := sha256Hex(name)
	pathH := sha256Hex(fullPath)
	contentH := sha256Hex(content)
	snapPayload := fmt.Sprintf("parent=%d|dir=%d|name=%s|path=%s|content=%s", parentID, dirFlag, nameH, pathH, contentH)
	return NodeHashes{
		NameHash:     nameH,
		PathHash:     pathH,
		ContentHash:  contentH,
		SnapshotHash: sha256Hex(snapPayload),
	}
}
