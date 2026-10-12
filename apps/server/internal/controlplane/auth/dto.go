package auth

import "mako/internal/controlplane/persistence/repo"

// UserDTO is the only shape a user row may take crossing into an HTTP
// response. Never add a field here by spreading a repo.User row — add it
// explicitly, after confirming it's safe to expose (never passwordHash).
type UserDTO struct {
	ID          string  `json:"id"`
	Type        string  `json:"type"`
	Username    *string `json:"username"`
	DisplayName string  `json:"displayName"`
	Status      string  `json:"status"`
}

// toUserDTO is the ONE mapping function from a repo.User row to the DTO
// shape that may cross into an HTTP response. Every place a repo.User row
// needs to become JSON-serializable must call this, never copy fields ad
// hoc, so the password hash can never accidentally leak into a response.
func toUserDTO(row repo.User) UserDTO {
	var username *string
	if row.Username.Valid {
		u := row.Username.String
		username = &u
	}
	return UserDTO{
		ID:          row.ID,
		Type:        row.UserType,
		Username:    username,
		DisplayName: row.DisplayName,
		Status:      row.Status,
	}
}
