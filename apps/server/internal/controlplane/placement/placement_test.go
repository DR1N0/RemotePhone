package placement

import (
	"errors"
	"testing"
)

func TestSelectorValidateAcceptsLocalOnly(t *testing.T) {
	s := NewSelector()
	if err := s.Validate(Policy{Kind: "local-only"}); err != nil {
		t.Errorf("Validate(local-only) = %v, want nil", err)
	}
}

func TestSelectorValidateRejectsUnsupportedKinds(t *testing.T) {
	s := NewSelector()
	for _, kind := range []string{"best-fit", "specific-worker", ""} {
		err := s.Validate(Policy{Kind: kind})
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("Validate(%q) error = %v, want ErrUnsupported", kind, err)
		}
	}
}
