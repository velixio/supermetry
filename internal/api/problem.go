package api

import (
	"encoding/json"
	"net/http"
)

func newProblem(status int, problemType, title, detail string) Problem {
	return Problem{
		Type:   problemType,
		Title:  title,
		Status: status,
		Detail: &detail,
	}
}

func writeProblem(w http.ResponseWriter, problem Problem) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(problem.Status)
	_ = json.NewEncoder(w).Encode(problem)
}

func badRequest(detail string) Problem {
	return newProblem(http.StatusBadRequest, "https://panel.dev/problems/bad-request", "Bad request", detail)
}

func unauthorized() Problem {
	return newProblem(http.StatusUnauthorized, "https://panel.dev/problems/unauthorized", "Unauthorized", "valid authentication is required")
}

func forbidden(detail string) Problem {
	return newProblem(http.StatusForbidden, "https://panel.dev/problems/forbidden", "Forbidden", detail)
}

func rateLimited() Problem {
	return newProblem(http.StatusTooManyRequests, "https://panel.dev/problems/rate-limited", "Rate limit exceeded", "retry after the indicated delay")
}

func internalProblem() Problem {
	return newProblem(http.StatusInternalServerError, "https://panel.dev/problems/internal", "Internal server error", "the request could not be completed")
}

func notFound(detail string) Problem {
	return newProblem(http.StatusNotFound, "https://panel.dev/problems/not-found", "Not found", detail)
}
