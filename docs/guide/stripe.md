# Stripe revenue

Supermetry can record payments made in Stripe as revenue: Checkout purchases, subscription renewals and refunds. A purchase is attributed to the source, channel and campaign of the visit that led to it, without cookies. Stripe sends signed webhooks to Supermetry; you don't give Supermetry a Stripe API key.

## Before you start

- The site needs a revenue currency (chosen at setup; older sites choose it once in Settings → General, see [project currency](../project-currency.md)). Only payments in that currency are recorded; others are skipped and shown as an error on the Stripe card.
- Attribution to a visit needs identified tracking mode (`SUPERMETRY_TRACKING_MODE=identified`) and profiles enabled for the site. Without them, payments are still recorded, but as "Direct / none". See [Privacy](#privacy).
- `SUPERMETRY_BASE_URL` should be the public URL of your instance, so the dashboard shows the full webhook URL.

## Set up

1. In Supermetry, open the site's Settings → Integrations and select **Enable Stripe** (owners and admins).
2. Copy the webhook URL. In the Stripe Dashboard, open Developers → Webhooks → Add endpoint, paste the URL and select these events:
   - `checkout.session.completed`
   - `invoice.paid`
   - `charge.refunded`
   - `payment_intent.succeeded` only if you take payments without Checkout or invoices (see below).
3. After creating the endpoint, reveal its signing secret (`whsec_…`) in Stripe, paste it into Supermetry and save. It is stored encrypted and never shown again. To roll the secret, paste the new one.
4. Send a test event (Stripe Dashboard or `stripe trigger checkout.session.completed`). The card shows the last event. Test-mode events are checked but never recorded as revenue.

## Attribute purchases

Ask Supermetry for an attribution id in the browser and pass it to Stripe as `client_reference_id` when you create the Checkout Session.

```ts
import { attributionId, init } from "@supermetry/tracker";

init({ site: "YOUR_SITE_ID", host: "https://stats.example.com" });

async function checkout(priceId: string) {
  const id = await attributionId(); // "sm1_…" or null
  const response = await fetch("/create-checkout-session", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ priceId, supermetryId: id }),
  });
  const { url } = await response.json();
  window.location.href = url;
}
```

Without the npm package, call the endpoint directly. It needs the visitor's own browser request, because it uses the same address and browser signature as the tracker:

```js
const response = await fetch("https://stats.example.com/api/public/v1/attribution", {
  method: "POST",
  headers: { "content-type": "application/json" },
  body: JSON.stringify({ site: "YOUR_SITE_ID" }),
});
const { id } = await response.json();
```

On your server (Node, `stripe` package):

```ts
const session = await stripe.checkout.sessions.create({
  mode: "subscription",
  line_items: [{ price: priceId, quantity: 1 }],
  success_url: "https://example.com/thanks",
  client_reference_id: supermetryId ?? undefined,
  metadata: { supermetry_product: "Pro plan" },
  subscription_data: {
    metadata: { supermetry_user_id: user.id, supermetry_product: "Pro plan" },
  },
});
```

- `client_reference_id`: the attribution id. If you already use `client_reference_id` for something else, put the id in `metadata.supermetry_id` instead.
- `metadata.supermetry_user_id` (optional): your user id, the same value you pass to `identify`. The payment is added to that profile when the site keeps profiles.
- `metadata.supermetry_product` (optional): the product name shown in the revenue report's product breakdown. Stripe webhooks don't include line items, so Supermetry reads no other product data.

The id is valid for 24 hours and only on its site. It is `null` when the visitor has no visit recorded today, the site doesn't keep profiles, or the visitor sends Global Privacy Control or Do Not Track and the site respects them. Behind a [first-party proxy](ad-blockers.md), use the proxy's host so the request reaches Supermetry with the visitor's address; `@supermetry/tracker/proxy` forwards the endpoint.

## What is recorded

| Stripe event | In Supermetry |
|---|---|
| `checkout.session.completed`, paid | `Stripe: Purchase` with the amount total |
| `invoice.paid` for a renewal | `Stripe: Renewal` with the amount paid. The first invoice of a subscription is not counted again |
| `charge.refunded` | Refund ledger, shown on the Stripe card (count and amount over 30 days) |
| `payment_intent.succeeded` | `Stripe: Payment`, only when enabled on the card and only for payments outside invoices |

Revenue events are custom events with revenue, like `track("Purchase", {}, { revenue })`, so they appear in the revenue report, revenue goals, funnels and profiles. Each delivery is handled once: Stripe's retries and resends are recognised by event id.

Amounts are converted from Stripe's minor units, including zero-decimal currencies (JPY, KRW and others: `500` is ¥500) and three-decimal currencies (KWD, BHD, JOD, OMR, TND).

### Renewals

When a checkout is attributed, Supermetry remembers the source, medium, campaign, country and product for that Stripe customer (under a keyed hash of the customer id). Later renewals for the customer are recorded with the same channel, so subscription revenue keeps its original source. They count as revenue, not as new visits. The stored channel is deleted after 400 days without a payment, or when you disconnect.

### Refunds

The revenue report shows gross revenue: refunds are not subtracted, because negative revenue would distort orders and average order value. The Stripe card lists refunds from the last 30 days.

### Payment intents

Checkout and invoices create payment intents too, so recording `payment_intent.succeeded` alongside them would count purchases twice. Turn it on only when you charge with Payment Intents directly. Intents that belong to an invoice are always skipped.

## Privacy

- No cookies. The attribution id is a short-lived encrypted token with the visit's session, channel and country, the same data the visitor's own events store. It cannot link visits across days.
- Supermetry keeps no customer email, name, address, phone, or Stripe customer or payment ids. Customer and charge ids are stored only as keyed hashes for renewals, refunds and de-duplication.
- In anonymous mode, or for sites without profiles, no attribution id is issued and no customer channel is stored: payments are recorded as unattributed revenue.

## Disconnect

Select **Disconnect Stripe** on the card. Supermetry deletes the webhook endpoint, the signing secret, the stored customer channels, the refund ledger and the list of handled events. Revenue already recorded stays. Delete the endpoint in Stripe as well; deliveries to a removed endpoint are refused.

## Troubleshooting

- **"the site has no revenue currency"**: choose it in Settings → General.
- **"payment in USD was not recorded: the site's revenue currency is EUR"**: Supermetry does not convert currencies.
- **Stripe shows 400 responses**: the signing secret doesn't match the endpoint, or the server clock is off by more than 5 minutes.
- **Purchases show as "Direct / none"**: the Checkout Session had no attribution id, the id was older than 24 hours, or the site doesn't keep profiles.
