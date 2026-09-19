package ids

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
)

var uuidRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func New(prefix string) string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b)
}

// UUID is a RFC 4122 v4 id. ComfyUI 0.3.7+ rejects non-UUID prompt_id.
func UUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func IsUUID(s string) bool { return uuidRE.MatchString(s) }

func KeyPrefix(plain string) string {
	if len(plain) > 11 {
		return plain[:11]
	}
	return plain
}
