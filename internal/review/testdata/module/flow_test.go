package fixture

import (
	"os"
	"testing"
)

func TestPaths(t *testing.T) {
	Nested()
	Indirect()
	PanicRecovered()
	Async()
	Thing{}.Run()
}

func TestExplicitOnly(t *testing.T) {
	if p := os.Getenv("GO_REVIEW_TEST_MARKER"); p != "" {
		if err := os.WriteFile(p, []byte("executed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	Thing{}.Run()
	UseGeneric()
	if os.Getenv("GO_REVIEW_TEST_FAIL") == "1" {
		t.Fatal("deliberate fixture failure")
	}
}
