package fixtures

import (
	"testing"
)

func TestCounterStruct(t *testing.T) {
	c := NewCounterStruct()
	if val := c.Get(); val != 0 {
		t.Fatalf("expected 0, got %d", val)
	}
}
