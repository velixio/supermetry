package ghstars

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// DefaultMaxWebhookBody caps the request body; star payloads are a few KiB.
const DefaultMaxWebhookBody = 1 << 20

// Handler receives GitHub repository webhooks on /api/github/webhook/{public_id}.
// The public id selects the repository; the body is only parsed after its
// HMAC-SHA256 signature verifies against that repository's secret.
type Handler struct {
	Service *Service
	// MaxBody caps the request body in bytes. Zero means DefaultMaxWebhookBody.
	MaxBody int64
}

type starPayload struct {
	Action     string     `json:"action"`
	StarredAt  *time.Time `json:"starred_at"`
	Repository struct {
		FullName   string `json:"full_name"`
		Stars      int    `json:"stargazers_count"`
		Forks      int    `json:"forks_count"`
		OpenIssues int    `json:"open_issues_count"`
	} `json:"repository"`
	Sender struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"sender"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	repo, err := h.Service.byPublicID(r.Context(), r.PathValue("public_id"))
	if err != nil || !repo.Enabled {
		if err != nil && !errors.Is(err, ErrNotFound) {
			slog.Error("look up github star repository", "error", err)
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
		return
	}
	secret, err := h.Service.open(repo.secretEnc)
	if err != nil {
		slog.Error("decrypt github webhook secret", "repo_id", repo.ID)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	limit := h.MaxBody
	if limit <= 0 {
		limit = DefaultMaxWebhookBody
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	if !validSignature([]byte(secret), body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}
	now := h.Service.Now()
	if _, err := h.Service.pool.Exec(r.Context(), `update github_star_repos set last_webhook_at=$2 where id=$1`, repo.ID, now); err != nil {
		slog.Error("record github webhook time", "error", err)
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		w.WriteHeader(http.StatusOK)
	case "star":
		h.star(w, r, body, h.Service.storeOf(repo), now)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

func (h *Handler) star(w http.ResponseWriter, r *http.Request, body []byte, store *Store, now time.Time) {
	delivery := r.Header.Get("X-GitHub-Delivery")
	var p starPayload
	if delivery == "" || json.Unmarshal(body, &p) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if p.Action != "created" && p.Action != "deleted" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if !strings.EqualFold(p.Repository.FullName, store.Repo()) {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if p.Sender.ID == 0 || p.Sender.Login == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	event := Event{DeliveryID: delivery, UserID: p.Sender.ID, Login: p.Sender.Login, Source: SourceWebhook, OccurredAt: now}
	if p.Action == "created" {
		event.Action = ActionStarred
		// starred_at is null for unstars, so only stars carry their own time.
		if p.StarredAt != nil {
			event.OccurredAt = *p.StarredAt
		}
	} else {
		event.Action = ActionUnstarred
	}
	counts := Counts{Stars: p.Repository.Stars, Forks: p.Repository.Forks, Watchers: KeepWatchers, OpenIssues: p.Repository.OpenIssues}
	if _, err := store.RecordEvent(r.Context(), event, counts, now); err != nil {
		slog.Error("record github star event", "error", err)
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	// A duplicate delivery is acknowledged like a new one so GitHub stops retrying.
	w.WriteHeader(http.StatusOK)
}

// validSignature checks a "sha256=<hex>" header against the body in constant time.
func validSignature(secret, body []byte, header string) bool {
	hexSum, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
