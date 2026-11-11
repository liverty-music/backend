package pb_test

import (
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// The tests in this file check, for each state enum of an entity, that every
// value the entity defines reaches the wire as its own proto value: a value
// the mapping forgets would become UNSPECIFIED, which a client reads as "no
// state". Each table lists every value of its enum except the zero value.

func TestOrderStatusToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.OrderStatus]entityv1.OrderStatus{
		entity.OrderStatusPaid:     entityv1.OrderStatus_ORDER_STATUS_PAID,
		entity.OrderStatusRefunded: entityv1.OrderStatus_ORDER_STATUS_REFUNDED,
		entity.OrderStatusPending:  entityv1.OrderStatus_ORDER_STATUS_PENDING,
		entity.OrderStatusVoid:     entityv1.OrderStatus_ORDER_STATUS_VOID,
	}
	for status, want := range tests {
		assert.Equal(t, want, pb.FromOrder(&entity.Order{Status: status}).GetStatus())
	}
	assert.Equal(t, entityv1.OrderStatus_ORDER_STATUS_UNSPECIFIED, pb.FromOrder(&entity.Order{}).GetStatus())
}

func TestPaymentProviderToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.PaymentProvider]entityv1.PaymentProvider{
		entity.PaymentProviderStripe: entityv1.PaymentProvider_PAYMENT_PROVIDER_STRIPE,
		entity.PaymentProviderKOMOJU: entityv1.PaymentProvider_PAYMENT_PROVIDER_KOMOJU,
	}
	for provider, want := range tests {
		order := &entity.Order{Payment: entity.Payment{Provider: provider}}
		assert.Equal(t, want, pb.FromOrder(order).GetPayment().GetProvider())
	}
	assert.Equal(t, entityv1.PaymentProvider_PAYMENT_PROVIDER_UNSPECIFIED, pb.FromOrder(&entity.Order{}).GetPayment().GetProvider())
}

func TestTicketStatusToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.TicketStatus]entityv1.TicketStatus{
		entity.TicketStatusIssued: entityv1.TicketStatus_TICKET_STATUS_ISSUED,
		entity.TicketStatusVoided: entityv1.TicketStatus_TICKET_STATUS_VOIDED,
	}
	for status, want := range tests {
		assert.Equal(t, want, pb.FromTicket(&entity.Ticket{Status: status}).GetStatus())
	}
	assert.Equal(t, entityv1.TicketStatus_TICKET_STATUS_UNSPECIFIED, pb.FromTicket(&entity.Ticket{}).GetStatus())
}

// A TicketStock's state is derived from its counts, so each row is a stock
// in that state.
func TestTicketStockStateToProto(t *testing.T) {
	t.Parallel()
	tests := []struct {
		stock entity.TicketStock
		state entity.TicketStockState
		want  entityv1.TicketStockState
	}{
		{entity.TicketStock{Quantity: 10, SoldCount: 3, HeldCount: 2}, entity.TicketStockStateAvailable, entityv1.TicketStockState_TICKET_STOCK_STATE_AVAILABLE},
		{entity.TicketStock{Quantity: 10, SoldCount: 3, HeldCount: 7}, entity.TicketStockStateAllHeld, entityv1.TicketStockState_TICKET_STOCK_STATE_ALL_HELD},
		{entity.TicketStock{Quantity: 10, SoldCount: 10}, entity.TicketStockStateSoldOut, entityv1.TicketStockState_TICKET_STOCK_STATE_SOLD_OUT},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.state, tt.stock.State(), "the fixture is a stock in the state under test")
		assert.Equal(t, tt.want, pb.FromTicketStock(&tt.stock, false, false).GetState())
	}
}

func TestTicketSaleMethodMapping(t *testing.T) {
	t.Parallel()
	tests := map[entity.TicketSaleMethod]entityv1.TicketSaleMethod{
		entity.TicketSaleMethodFirstCome: entityv1.TicketSaleMethod_TICKET_SALE_METHOD_FIRST_COME,
		entity.TicketSaleMethodLottery:   entityv1.TicketSaleMethod_TICKET_SALE_METHOD_LOTTERY,
	}
	for method, want := range tests {
		got := pb.FromTicketSale(&entity.TicketSale{Method: method}).GetMethod()
		assert.Equal(t, want, got)
		assert.Equal(t, method, pb.ToTicketSaleMethod(got), "the method maps back to itself")
	}
	assert.Equal(t, entityv1.TicketSaleMethod_TICKET_SALE_METHOD_UNSPECIFIED, pb.FromTicketSale(&entity.TicketSale{}).GetMethod())
	assert.Equal(t, entity.TicketSaleMethodUnspecified, pb.ToTicketSaleMethod(entityv1.TicketSaleMethod_TICKET_SALE_METHOD_UNSPECIFIED))
}

// VerificationRequirementNone is the zero value and means "no verification
// required": it is UNSPECIFIED on the wire in both directions.
func TestVerificationRequirementMapping(t *testing.T) {
	t.Parallel()
	tests := map[entity.VerificationRequirement]entityv1.VerificationRequirement{
		entity.VerificationRequirementVerifiedAny: entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_VERIFIED_ANY,
		entity.VerificationRequirementJPKIOnly:    entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY,
	}
	for requirement, want := range tests {
		got := pb.FromTicketSale(&entity.TicketSale{VerificationRequirement: requirement}).GetVerificationRequirement()
		assert.Equal(t, want, got)
		assert.Equal(t, requirement, pb.ToVerificationRequirement(got), "the requirement maps back to itself")
	}
	none := pb.FromTicketSale(&entity.TicketSale{VerificationRequirement: entity.VerificationRequirementNone}).GetVerificationRequirement()
	assert.Equal(t, entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_UNSPECIFIED, none)
	assert.Equal(t, entity.VerificationRequirementNone, pb.ToVerificationRequirement(none))
}

func TestVerificationMethodMapping(t *testing.T) {
	t.Parallel()
	tests := map[entity.VerificationMethod]entityv1.VerificationMethod{
		entity.VerificationMethodJPKI:          entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI,
		entity.VerificationMethodDriverLicence: entityv1.VerificationMethod_VERIFICATION_METHOD_DRIVER_LICENCE,
	}
	for method, want := range tests {
		got := pb.FromIdentityVerification(&entity.IdentityVerification{Method: method}).GetMethod()
		assert.Equal(t, want, got)
		assert.Equal(t, method, pb.ToVerificationMethod(got), "the method maps back to itself")
	}
	assert.Equal(t, entityv1.VerificationMethod_VERIFICATION_METHOD_UNSPECIFIED, pb.FromIdentityVerification(&entity.IdentityVerification{}).GetMethod())
	assert.Equal(t, entity.VerificationMethodUnspecified, pb.ToVerificationMethod(entityv1.VerificationMethod_VERIFICATION_METHOD_UNSPECIFIED))
}

func TestDedupeStrengthToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.DedupeStrength]entityv1.DedupeStrength{
		entity.DedupeStrengthStrong: entityv1.DedupeStrength_DEDUPE_STRENGTH_STRONG,
		entity.DedupeStrengthWeak:   entityv1.DedupeStrength_DEDUPE_STRENGTH_WEAK,
	}
	for strength, want := range tests {
		assert.Equal(t, want, pb.FromIdentityVerification(&entity.IdentityVerification{DedupeStrength: strength}).GetDedupeStrength())
	}
	assert.Equal(t, entityv1.DedupeStrength_DEDUPE_STRENGTH_UNSPECIFIED, pb.FromIdentityVerification(&entity.IdentityVerification{}).GetDedupeStrength())
}

func TestVerificationStatusToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.VerificationStatus]entityv1.VerificationStatus{
		entity.VerificationStatusActive:              entityv1.VerificationStatus_VERIFICATION_STATUS_ACTIVE,
		entity.VerificationStatusNeedsReverification: entityv1.VerificationStatus_VERIFICATION_STATUS_NEEDS_REVERIFICATION,
	}
	for status, want := range tests {
		assert.Equal(t, want, pb.FromVerificationStatus(status))
		assert.Equal(t, want, pb.FromIdentityVerification(&entity.IdentityVerification{Status: status}).GetStatus())
	}
	assert.Equal(t, entityv1.VerificationStatus_VERIFICATION_STATUS_UNSPECIFIED, pb.FromVerificationStatus(entity.VerificationStatusUnspecified))
}

func TestSettlementStatusToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.SettlementStatus]entityv1.SettlementStatus{
		entity.SettlementStatusHeld:     entityv1.SettlementStatus_SETTLEMENT_STATUS_HELD,
		entity.SettlementStatusReleased: entityv1.SettlementStatus_SETTLEMENT_STATUS_RELEASED,
		entity.SettlementStatusReversed: entityv1.SettlementStatus_SETTLEMENT_STATUS_REVERSED,
	}
	for status, want := range tests {
		assert.Equal(t, want, pb.FromSettlement(&entity.Settlement{Status: status}).GetStatus())
	}
	assert.Equal(t, entityv1.SettlementStatus_SETTLEMENT_STATUS_UNSPECIFIED, pb.FromSettlement(&entity.Settlement{}).GetStatus())
}

func TestPayoutOnboardingStatusToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.PayoutOnboardingStatus]entityv1.PayoutOnboardingStatus{
		entity.PayoutOnboardingStatusPending:    entityv1.PayoutOnboardingStatus_PAYOUT_ONBOARDING_STATUS_PENDING,
		entity.PayoutOnboardingStatusActive:     entityv1.PayoutOnboardingStatus_PAYOUT_ONBOARDING_STATUS_ACTIVE,
		entity.PayoutOnboardingStatusRestricted: entityv1.PayoutOnboardingStatus_PAYOUT_ONBOARDING_STATUS_RESTRICTED,
	}
	for status, want := range tests {
		assert.Equal(t, want, pb.FromPayoutAccount(&entity.PayoutAccount{Status: status}).GetStatus())
	}
	assert.Equal(t, entityv1.PayoutOnboardingStatus_PAYOUT_ONBOARDING_STATUS_UNSPECIFIED, pb.FromPayoutAccount(&entity.PayoutAccount{}).GetStatus())
}

func TestScannerStatusToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.ScannerStatus]entityv1.ScannerStatus{
		entity.ScannerStatusUnused:  entityv1.ScannerStatus_SCANNER_STATUS_UNUSED,
		entity.ScannerStatusInUse:   entityv1.ScannerStatus_SCANNER_STATUS_IN_USE,
		entity.ScannerStatusRevoked: entityv1.ScannerStatus_SCANNER_STATUS_REVOKED,
	}
	for status, want := range tests {
		assert.Equal(t, want, pb.FromScanner(&entity.Scanner{Status: status}).GetStatus())
	}
	assert.Equal(t, entityv1.ScannerStatus_SCANNER_STATUS_UNSPECIFIED, pb.FromScanner(&entity.Scanner{}).GetStatus())
}

func TestRejectedScanReasonToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.RejectedScanReason]entityv1.RejectedScanReason{
		entity.RejectedScanReasonForged:          entityv1.RejectedScanReason_REJECTED_SCAN_REASON_FORGED,
		entity.RejectedScanReasonExpired:         entityv1.RejectedScanReason_REJECTED_SCAN_REASON_EXPIRED,
		entity.RejectedScanReasonOtherEvent:      entityv1.RejectedScanReason_REJECTED_SCAN_REASON_OTHER_EVENT,
		entity.RejectedScanReasonVoided:          entityv1.RejectedScanReason_REJECTED_SCAN_REASON_VOIDED,
		entity.RejectedScanReasonAlreadyAdmitted: entityv1.RejectedScanReason_REJECTED_SCAN_REASON_ALREADY_ADMITTED,
	}
	for reason, want := range tests {
		assert.Equal(t, want, pb.FromRejectedScanReason(reason))
	}
	assert.Equal(t, entityv1.RejectedScanReason_REJECTED_SCAN_REASON_UNSPECIFIED, pb.FromRejectedScanReason(entity.RejectedScanReasonUnspecified))
}

// A Series without a visibility stays UNSPECIFIED on the wire; an
// UNSPECIFIED visibility in a request is read as Public.
func TestSeriesVisibilityMapping(t *testing.T) {
	t.Parallel()
	tests := map[entity.SeriesVisibility]entityv1.Visibility{
		entity.SeriesVisibilityPublic:   entityv1.Visibility_VISIBILITY_PUBLIC,
		entity.SeriesVisibilityUnlisted: entityv1.Visibility_VISIBILITY_UNLISTED,
	}
	for visibility, want := range tests {
		got := pb.FromSeries(&entity.Series{Visibility: &visibility}).GetVisibility()
		assert.Equal(t, want, got)
		assert.Equal(t, visibility, pb.ToVisibility(got), "the visibility maps back to itself")
	}
	assert.Equal(t, entityv1.Visibility_VISIBILITY_UNSPECIFIED, pb.FromSeries(&entity.Series{}).GetVisibility())
	assert.Equal(t, entity.SeriesVisibilityPublic, pb.ToVisibility(entityv1.Visibility_VISIBILITY_UNSPECIFIED))
}

func TestPublishStateToProto(t *testing.T) {
	t.Parallel()
	tests := map[entity.PublishState]entityv1.PublishState{
		entity.PublishStateDraft:     entityv1.PublishState_PUBLISH_STATE_DRAFT,
		entity.PublishStatePublished: entityv1.PublishState_PUBLISH_STATE_PUBLISHED,
		entity.PublishStateCancelled: entityv1.PublishState_PUBLISH_STATE_CANCELLED,
	}
	for state, want := range tests {
		assert.Equal(t, want, pb.FromSeries(&entity.Series{PublishState: &state}).GetPublishState())
	}
	assert.Equal(t, entityv1.PublishState_PUBLISH_STATE_UNSPECIFIED, pb.FromSeries(&entity.Series{}).GetPublishState())
}

func TestCheckoutStatusMapping(t *testing.T) {
	t.Parallel()
	tests := map[entity.CheckoutStatus]entityv1.CheckoutStatus{
		entity.CheckoutStatusHeld:      entityv1.CheckoutStatus_CHECKOUT_STATUS_HELD,
		entity.CheckoutStatusCommitted: entityv1.CheckoutStatus_CHECKOUT_STATUS_COMMITTED,
		entity.CheckoutStatusCompleted: entityv1.CheckoutStatus_CHECKOUT_STATUS_COMPLETED,
		entity.CheckoutStatusExpired:   entityv1.CheckoutStatus_CHECKOUT_STATUS_EXPIRED,
		entity.CheckoutStatusReleased:  entityv1.CheckoutStatus_CHECKOUT_STATUS_RELEASED,
	}
	for status, want := range tests {
		message := pb.FromCheckout(&entity.Checkout{Status: status})
		assert.Equal(t, want, message.GetStatus())
		assert.Equal(t, status, pb.CheckoutFromAnnouncement(message).Status, "the status maps back to itself")
	}
	assert.Equal(t, entityv1.CheckoutStatus_CHECKOUT_STATUS_UNSPECIFIED, pb.FromCheckout(&entity.Checkout{}).GetStatus())
	assert.Equal(t, entity.CheckoutStatusUnspecified, pb.CheckoutFromAnnouncement(&entityv1.Checkout{}).Status)
}

func TestCreateTicketSaleInputFromProto(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC)
	end := time.Date(2026, 12, 20, 14, 59, 0, 0, time.UTC)

	t.Run("maps every field of the request", func(t *testing.T) {
		t.Parallel()
		got := pb.CreateTicketSaleInputFromProto(&organizerticketsalev1.CreateRequest{
			SeriesId:                &entityv1.SeriesId{Value: "series-1"},
			Name:                    &entityv1.TicketSaleName{Value: "先行抽選"},
			Method:                  entityv1.TicketSaleMethod_TICKET_SALE_METHOD_LOTTERY,
			SaleStartTime:           timestamppb.New(start),
			SaleEndTime:             timestamppb.New(end),
			VerificationRequirement: entityv1.VerificationRequirement_VERIFICATION_REQUIREMENT_JPKI_ONLY,
			EventIds:                []*entityv1.EventId{{Value: "event-1"}, {Value: "event-2"}},
		})

		assert.Equal(t, usecase.CreateTicketSaleInput{
			SeriesID:                "series-1",
			Name:                    "先行抽選",
			Method:                  entity.TicketSaleMethodLottery,
			SaleStart:               start,
			SaleEnd:                 end,
			VerificationRequirement: entity.VerificationRequirementJPKIOnly,
			EventIDs:                []string{"event-1", "event-2"},
		}, got)
	})

	t.Run("a request without a sale end leaves the end unset", func(t *testing.T) {
		t.Parallel()
		got := pb.CreateTicketSaleInputFromProto(&organizerticketsalev1.CreateRequest{
			SeriesId:      &entityv1.SeriesId{Value: "series-1"},
			Method:        entityv1.TicketSaleMethod_TICKET_SALE_METHOD_FIRST_COME,
			SaleStartTime: timestamppb.New(start),
		})

		assert.True(t, got.SaleEnd.IsZero(), "no end, not the Unix epoch")
		assert.Equal(t, entity.TicketSaleMethodFirstCome, got.Method)
		assert.Equal(t, entity.VerificationRequirementNone, got.VerificationRequirement)
		assert.Empty(t, got.EventIDs)
	})
}

func TestBusinessDetailsFromSetRequest(t *testing.T) {
	t.Parallel()

	t.Run("maps the five business details", func(t *testing.T) {
		t.Parallel()
		got := pb.BusinessDetailsFromSetRequest(
			&entityv1.LegalName{Value: "株式会社ライブ"},
			&entityv1.RepresentativeName{Value: "山田 太郎"},
			&entityv1.BusinessAddress{Value: "東京都渋谷区1-2-3"},
			&entityv1.PhoneNumber{Value: "+81312345678"},
			&entityv1.Email{Value: "contact@example.com"},
		)

		assert.Equal(t, entity.BusinessDetails{
			LegalName:           "株式会社ライブ",
			RepresentativeName:  "山田 太郎",
			BusinessAddress:     "東京都渋谷区1-2-3",
			BusinessPhoneNumber: "+81312345678",
			ContactEmail:        "contact@example.com",
		}, got)
	})

	t.Run("a detail the request leaves out is empty", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, entity.BusinessDetails{}, pb.BusinessDetailsFromSetRequest(nil, nil, nil, nil, nil))
	})
}
