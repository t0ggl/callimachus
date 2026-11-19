package metadata_test

import (
	"testing"

	"github.com/t0ggl/callimachus/pkg/metadata"
)

func TestNormalizeISBN13(t *testing.T) {
	for _, test := range []struct {
		input, want string
	}{
		{" URN:ISBN:978-0-8044-2957-3 ", "9780804429573"},
		{"0-8044 2957-x", "9780804429573"},
		{"0140328726", "9780140328721"},
		{"9791090636071", "9791090636071"},
		{"9780804429570", ""},
		{"0804429570", ""},
		{"08044X9570", ""},
		{"4006381333931", ""},
		{"978080442957?", ""},
		{"", ""},
	} {
		if got := metadata.NormalizeISBN13(test.input); got != test.want {
			t.Errorf("NormalizeISBN13(%q) = %q, want %q", test.input, got, test.want)
		}
	}
}
