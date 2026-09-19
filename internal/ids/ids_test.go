package ids

import "testing"

func TestUUID(t *testing.T) {
	u := UUID()
	if !IsUUID(u) {
		t.Fatalf("%q", u)
	}
	if IsUUID("img_abc") || IsUUID("video_01JQC") {
		t.Fatal("prefix ids must not count as UUID")
	}
}
