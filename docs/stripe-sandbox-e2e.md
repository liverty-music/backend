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

There are **two** Stripe contexts, not one per deployment environment. Keys live in
env/secret vaults, **never** in source.

| Context | Stripe account | Key location |
|---|---|---|
| test — local | `local` sandbox | shell env / gitignored `.env` |
| test — ci | `ci` sandbox | GitHub Actions repo secret `STRIPE_TEST_SECRET_KEY` |
| prod | the real account (test mode until the livemode flip) | Pulumi ESC → GSM → ESO (`esc env set liverty-music/prod pulumiConfig.stripeSecretKey`) |

> **`dev` has no Stripe key, by design.** `pulumiConfig.stripeSecretKey` is unset for
> `liverty-music/dev`, so the backend there runs `NoopAuthorizationPort` and the Stripe
> webhook handler fails closed (503). There is no dev Stripe environment and therefore no
> dev webhook endpoint to register — prod's is the only endpoint ever registered with
> Stripe, and that happens at launch. Verify webhook handling locally by forwarding events
> with the Stripe CLI (below) instead of pointing Stripe at a deployed URL. See the
> `ticket-settlement-and-payout` design decision "Stripe environments: prod + test only".

> A previous revision of this file said accounts are capped at one sandbox until business
> verification, and advised pointing every environment at a single shared sandbox. That no
> longer applies: the named `local` and `ci` sandboxes both exist.

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

## Exercise inbound webhooks locally

The suites above drive Stripe outbound (charge, transfer, payout). The inbound half —
refund/dispute events reaching `StripeWebhookHandler`, and the idempotency of a repeated
delivery — needs Stripe to call us. Since no deployed environment has a registered
endpoint (see **Environment sandboxes**), forward events to the local webhook server with
the Stripe CLI instead:

```bash
# Terminal 1 — run the API server; the webhook listener binds :9090
make run   # or however you start the server locally

# Terminal 2 — forward sandbox events to it. The CLI prints a whsec_... on startup;
# export it so the handler can verify signatures, then restart the server.
stripe listen --api-key rk_test_… \
  --forward-to localhost:9090/stripe-webhook \
  --events charge.refunded,charge.dispute.created,charge.dispute.closed,\
transfer.created,transfer.reversed,payout.paid,payout.failed

# Terminal 3 — fire an event, twice, to check idempotency
stripe trigger charge.dispute.created --api-key rk_test_…
```

Notes:

- `stripe listen` mints its **own** signing secret, unrelated to the one Pulumi provisions
  for prod. Put it in `STRIPE_WEBHOOK_SIGNING_SECRET` for the local run only.
- With that variable unset the handler returns 503 by design, so an empty `whsec_` is a
  configuration mistake, not a silent pass.
- Replaying the same event must not double-refund or double-reverse — that is the
  idempotency leg of `ticket-settlement-and-payout` §6.1.
