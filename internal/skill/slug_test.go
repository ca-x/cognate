package skill

import (
	"strings"
	"testing"
)

func TestPortableSlugsAreCrossPlatform(test *testing.T) {
	for _, name := range []string{"Shared Skill", "CON", "LPT9", "../outside", "你好", strings.Repeat("a", 256)} {
		value := PortableSlug(name)
		if ValidatePortableSlug(value) != nil || value != PortableSlug(name) {
			test.Fatalf("invalid or unstable slug for %q", name)
		}
	}
	if PortableSlug("Shared Skill") != "shared-skill" {
		test.Fatal("unexpected slug")
	}
	for _, value := range []string{"Foo", "CON", "con", "lpt1", "a.", "../path", "a/b", "a\\b", "你好", "a ", "-a", "a-"} {
		if ValidatePortableSlug(value) == nil {
			test.Fatalf("unsafe alias accepted: %q", value)
		}
	}
}
