package stripe

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// Event names recorded in Supermetry.
const (
	EventPurchase = "Stripe: Purchase"
	EventRenewal  = "Stripe: Renewal"
	EventPayment  = "Stripe: Payment"
)

// Metadata keys read from Stripe objects. Everything else in the payload,
// including customer emails, names and addresses, is ignored.
const (
	MetadataAttribution = "supermetry_id"
	MetadataProfile     = "supermetry_user_id"
	MetadataProduct     = "supermetry_product"
)

// HandledEvents are the event types to select in the Stripe dashboard.
// payment_intent.succeeded is only needed when payments do not go through
// Checkout or Invoices and recording it is switched on.
var HandledEvents = []string{
	"checkout.session.completed",
	"checkout.session.async_payment_succeeded",
	"invoice.paid",
	"charge.refunded",
}

// OptionalEvents are handled only when the connection opts in.
var OptionalEvents = []string{"payment_intent.succeeded"}

const (
	maxProfileChars = 128
	maxProductChars = 100
	maxObjectID     = 255
)

var errEnvelope = errors.New("stripe event is malformed")

// Envelope is the part of a Stripe event that every type shares.
type Envelope struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Created  int64  `json:"created"`
	Livemode bool   `json:"livemode"`
	Data     struct {
		Object json.RawMessage `json:"object"`
	} `json:"data"`
}

// ParseEnvelope decodes and validates the event envelope.
func ParseEnvelope(body []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(body, &e); err != nil {
		return Envelope{}, errEnvelope
	}
	if !strings.HasPrefix(e.ID, "evt_") || len(e.ID) > maxObjectID || !validTypeName(e.Type) {
		return Envelope{}, errEnvelope
	}
	return e, nil
}

func validTypeName(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_') {
			return false
		}
	}
	return true
}

// ActionKind is what a Stripe event turns into.
type ActionKind int

const (
	// ActionIgnore records nothing beyond the delivery status.
	ActionIgnore ActionKind = iota
	// ActionRevenue writes a revenue event.
	ActionRevenue
	// ActionCustomer remembers the customer's channel without revenue, e.g. a
	// subscription that starts with a free trial.
	ActionCustomer
	// ActionRefund updates the refund ledger.
	ActionRefund
)

// Action is the privacy-filtered result of mapping one Stripe event.
type Action struct {
	Kind        ActionKind
	Name        string
	AmountMinor int64
	Currency    string
	// Token is the attribution token from client_reference_id or metadata.
	Token string
	// Profile is metadata.supermetry_user_id, the site's own user id.
	Profile  string
	Product  string
	Customer string
	// ObjectID is the Stripe object the action is about (session, invoice,
	// charge or payment intent). It is only ever stored as a keyed hash.
	ObjectID string
	// Renewal attributes the event to the channel stored for the customer.
	Renewal bool
	Props   map[string]string
	// Skip explains an ignored event in the connection status.
	Skip string
}

// MapOptions are the per-connection choices.
type MapOptions struct {
	RecordPaymentIntents bool
}

type stripeObject struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"`
	Mode              string            `json:"mode"`
	PaymentStatus     string            `json:"payment_status"`
	AmountTotal       *int64            `json:"amount_total"`
	AmountPaid        *int64            `json:"amount_paid"`
	AmountReceived    *int64            `json:"amount_received"`
	AmountRefunded    *int64            `json:"amount_refunded"`
	Currency          string            `json:"currency"`
	ClientReferenceID *string           `json:"client_reference_id"`
	Customer          json.RawMessage   `json:"customer"`
	Invoice           json.RawMessage   `json:"invoice"`
	Subscription      json.RawMessage   `json:"subscription"`
	BillingReason     string            `json:"billing_reason"`
	Metadata          map[string]string `json:"metadata"`
	// Invoice subscription metadata: subscription_details before API version
	// 2025-03-31, parent.subscription_details after.
	SubscriptionDetails *subscriptionDetails `json:"subscription_details"`
	Parent              *struct {
		SubscriptionDetails *subscriptionDetails `json:"subscription_details"`
	} `json:"parent"`
}

type subscriptionDetails struct {
	Subscription json.RawMessage   `json:"subscription"`
	Metadata     map[string]string `json:"metadata"`
}

// MapEvent turns a verified Stripe event into an Action. Only allow-listed
// fields are read; unknown types are ignored.
func MapEvent(e Envelope, options MapOptions) (Action, error) {
	switch e.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded",
		"invoice.paid", "charge.refunded", "payment_intent.succeeded":
	default:
		return Action{Skip: "event type is not used"}, nil
	}
	var object stripeObject
	if err := json.Unmarshal(e.Data.Object, &object); err != nil {
		return Action{}, errEnvelope
	}
	if object.ID == "" || len(object.ID) > maxObjectID {
		return Action{}, errEnvelope
	}
	action := Action{
		ObjectID: object.ID,
		Customer: objectID(object.Customer),
		Currency: object.Currency,
		Profile:  metadataValue(object.Metadata, MetadataProfile, maxProfileChars),
		Product:  metadataValue(object.Metadata, MetadataProduct, maxProductChars),
		Props:    map[string]string{"stripe_event_type": e.Type},
	}
	switch e.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		action.Name = EventPurchase
		if object.ClientReferenceID != nil && strings.HasPrefix(*object.ClientReferenceID, TokenPrefix) {
			action.Token = *object.ClientReferenceID
		} else if token := object.Metadata[MetadataAttribution]; strings.HasPrefix(token, TokenPrefix) {
			action.Token = token
		}
		if object.Mode != "" {
			action.Props["stripe_mode"] = object.Mode
		}
		switch {
		case object.PaymentStatus == "paid" && object.AmountTotal != nil && *object.AmountTotal > 0:
			action.Kind, action.AmountMinor = ActionRevenue, *object.AmountTotal
		case object.PaymentStatus == "unpaid":
			// Delayed payment methods: the async_payment_succeeded event records it.
			action.Skip = "payment is not complete yet"
			if action.Customer != "" {
				action.Kind = ActionCustomer
			}
		default:
			action.Skip = "checkout had nothing to pay"
			if action.Customer != "" {
				action.Kind = ActionCustomer
			}
		}
	case "invoice.paid":
		action.Name, action.Renewal = EventRenewal, true
		details := object.SubscriptionDetails
		if object.Parent != nil && object.Parent.SubscriptionDetails != nil {
			details = object.Parent.SubscriptionDetails
		}
		subscription := objectID(object.Subscription)
		if details != nil {
			if id := objectID(details.Subscription); id != "" {
				subscription = id
			}
			if action.Profile == "" {
				action.Profile = metadataValue(details.Metadata, MetadataProfile, maxProfileChars)
			}
			if action.Product == "" {
				action.Product = metadataValue(details.Metadata, MetadataProduct, maxProductChars)
			}
		}
		if object.BillingReason != "" {
			action.Props["billing_reason"] = object.BillingReason
		}
		switch {
		case subscription == "":
			action.Skip = "invoice is not part of a subscription"
		case object.BillingReason == "subscription_create":
			// The first invoice is the checkout itself, recorded as a purchase.
			action.Skip = "first subscription invoice is recorded by checkout"
		case object.AmountPaid == nil || *object.AmountPaid <= 0:
			action.Skip = "invoice had nothing to pay"
		default:
			action.Kind, action.AmountMinor = ActionRevenue, *object.AmountPaid
		}
	case "charge.refunded":
		if object.AmountRefunded == nil || *object.AmountRefunded <= 0 {
			action.Skip = "charge has no refunded amount"
			break
		}
		action.Kind, action.AmountMinor = ActionRefund, *object.AmountRefunded
	case "payment_intent.succeeded":
		action.Name = EventPayment
		action.Token = metadataValue(object.Metadata, MetadataAttribution, 256)
		switch {
		case !options.RecordPaymentIntents:
			action.Skip = "payment intents are not recorded"
		case objectID(object.Invoice) != "":
			action.Skip = "invoice payments are recorded by invoice.paid"
		case object.AmountReceived == nil || *object.AmountReceived <= 0:
			action.Skip = "payment intent received nothing"
		default:
			action.Kind, action.AmountMinor = ActionRevenue, *object.AmountReceived
		}
	}
	if action.Product != "" {
		action.Props["product"] = action.Product
	}
	if action.Kind == ActionIgnore {
		action.Props = nil
	}
	return action, nil
}

// objectID reads a Stripe reference that is either an id string or an
// expanded object with an id.
func objectID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var id string
	if err := json.Unmarshal(raw, &id); err == nil {
		if len(id) > maxObjectID {
			return ""
		}
		return id
	}
	var expanded struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &expanded); err != nil || len(expanded.ID) > maxObjectID {
		return ""
	}
	return expanded.ID
}

func metadataValue(metadata map[string]string, key string, maxChars int) string {
	value := strings.TrimSpace(metadata[key])
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxChars {
		return ""
	}
	return value
}
