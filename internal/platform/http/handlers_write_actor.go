package platformhttp

import "panel/internal/platform/authz"

func createdByUser(principal authz.Principal) any {
	if principal.UserID == 0 {
		return nil
	}
	return principal.UserID
}
