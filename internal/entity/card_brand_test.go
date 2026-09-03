package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestIsAcceptedCardBrand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		brand entity.CardBrand
		want  bool
	}{
		{brand: "visa", want: true},
		{brand: "mastercard", want: true},
		{brand: "jcb", want: true},
		{brand: "diners", want: true},
		{brand: "discover", want: true},
		{brand: entity.CardBrandAmex, want: false},
		{brand: "", want: true},         // unknown/unclassified brand (nil card details) is accepted
		{brand: "unionpay", want: true}, // any other brand is accepted
	}

	for _, tc := range cases {
		name := string(tc.brand)
		if name == "" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, entity.IsAcceptedCardBrand(tc.brand))
		})
	}
}
