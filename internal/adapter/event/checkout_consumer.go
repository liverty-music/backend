package event

import (
	"errors"
	"fmt"
	"log/slog"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/liverty-music/backend/pkg/eventmsg"
	"github.com/pannpers/go-logging/logging"
)

// CheckoutConsumer handles CHECKOUT.held, CHECKOUT.expired and
// CHECKOUT.released events, which Checkout.GetOrCreateHeld,
// Checkout.Release and Checkout.RevertCommit record, by starting the workflow
// that follows the Checkout: the expiry of a held one and the release of an
// ended one's card authorization. Each workflow is named after the Checkout or
// its Order, so a redelivery starts nothing twice.
type CheckoutConsumer struct {
	checkoutUC usecase.CheckoutUseCase
	logger     *logging.Logger
}

// NewCheckoutConsumer creates a new CheckoutConsumer.
func NewCheckoutConsumer(checkoutUC usecase.CheckoutUseCase, logger *logging.Logger) *CheckoutConsumer {
	return &CheckoutConsumer{checkoutUC: checkoutUC, logger: logger}
}

// HandleStartWorkflow starts the workflow that follows the announced
// Checkout.
func (h *CheckoutConsumer) HandleStartWorkflow(msg *message.Message) error {
	var c entityv1.Checkout
	if err := eventmsg.ParseProtobufData(msg, &c); err != nil {
		h.logger.Error(msg.Context(), "failed to parse a checkout announcement", err)
		return fmt.Errorf("parse a checkout announcement: %w", err)
	}
	checkout := pb.CheckoutFromAnnouncement(&c)
	if checkout.ID == "" || checkout.OrderID == "" {
		err := errors.New("the Checkout has no id or no Order")
		h.logger.Error(msg.Context(), "failed to parse a checkout announcement", err)
		return err
	}
	h.logger.Info(msg.Context(), "processing a checkout announcement for its workflow",
		slog.String("checkout_id", string(checkout.ID)),
		slog.String("status", checkout.Status.String()))
	if err := h.checkoutUC.StartCheckoutWorkflow(msg.Context(), checkout); err != nil {
		return fmt.Errorf("start the workflow of checkout %s: %w", checkout.ID, err)
	}
	return nil
}
