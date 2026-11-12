package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
)

func TestTransition_Changed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   entity.Transition[entity.OrderStatus]
		want bool
	}{
		{"zero transition", entity.Transition[entity.OrderStatus]{}, false},
		{"a target without a source", entity.Transition[entity.OrderStatus]{To: entity.OrderStatusVoid}, false},
		{"one source", entity.Transition[entity.OrderStatus]{From: []entity.OrderStatus{entity.OrderStatusPending}, To: entity.OrderStatusVoid}, true},
		{"several sources", entity.Transition[entity.OrderStatus]{From: []entity.OrderStatus{entity.OrderStatusPending, entity.OrderStatusPaid}, To: entity.OrderStatusRefunded}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.in.Changed())
		})
	}
}
