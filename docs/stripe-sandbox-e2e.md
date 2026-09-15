# Stripe sandbox (test-mode) E2E

End-to-end checks for the ticket payment/issuance/settlement stack, run against a
Stripe **sandbox (test mode)**. **No real money moves** — charges use test cards
(`pm_card_visa`), balances are simulated, and `test`/`live` keys and data are
fully isolated. A `test`-mode key can never touch livemode.

## What it verifies

`make test-stripe-e2e` runs two opt-in suites in `internal/infrastructure/payment/`:

| Suite | Leg | Asserts |
|---|---|---|
| `TestStripeAuthorizationPort_Integration` (④) | authorize hold → confirm (test card) → capture / cancel | manual-capture hold model + deterministic idempotency-key no-op on retried capture/cancel; amount-mismatch verify rejects |
| `TestStripeConnect_Settlement_PoC` (money-out) | self-provision JP recipient account → platform charge → transfer (`source_transaction`) → payout | separate charges & transfers; platform fee kept as the non-transferred remainder (no `application_fee_amount`); payout best-effort in test mode |

The ⑤ issuance logic (`IssueFromCapturedWin` → Order + N tickets → ticket-journey
PAID, idempotency, not-Won guard) is covered by the DB-backed unit/integration
tests (`make test`); this harness covers the Stripe-facing legs those mock.

## Environment sandboxes

One Stripe sandbox per environment, each with its own `rk_test_` key. Keys live in
env/secret vaults, **never** in source.

| Env | Sandbox | Key location |
|---|---|---|
| local | `local` (or the existing shared dev sandbox) | shell env / gitignored `.env` |
| ci | `ci` | GitHub Actions repo secret `STRIPE_TEST_SECRET_KEY` |
| dev / staging / prod | per-env test-mode | Pulumi ESC → GSM → ESO (`esc env set liverty-music/<env> pulumiConfig.stripeSecretKey`) |

> Creating additional **named** sandboxes under one account requires business
> verification (accounts are capped at one sandbox until verified). Until then,
> point every environment at the one existing sandbox, or provision independent
> sandboxes via the Dashboard.

## Get a restricted key

In the sandbox's API keys page (`dashboard.stripe.com/<acct>/test/apikeys`), create a
**restricted key** with **Write** on: PaymentIntents, Charges, Transfers, Payouts,
and the **Connect** section (covers connected-account create + Persons + External
Accounts); **Read** on Balance. Copy the `rk_test_…` once (shown only at creation).

## Run

```bash
# local
export STRIPE_SECRET_KEY=rk_test_…          # test-mode restricted key
make test-stripe-e2e                        # ④ integration + Connect money-out PoC

# money-out PoC only, with the optional payout leg
STRIPE_CONNECT_POC=1 STRIPE_CONNECT_POC_PAYOUT=1 STRIPE_SECRET_KEY=rk_test_… \
  go test -run Settlement_PoC -v ./internal/infrastructure/payment/

# reuse a pre-onboarded connected account instead of self-provisioning
STRIPE_CONNECT_ACCOUNT=acct_… …
```

Both suites are opt-in: with no key set they `t.Skip`, so `make check` and CI stay
network-free.
