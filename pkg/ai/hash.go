package ai

import (
	"crypto/md5"
	"encoding/hex"
)

// md5HexImpl computes the MD5 hash of s and returns it as a hex string.
func md5HexImpl(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
