package admin

import (
	"database/sql"

	"remotephone/internal/controlplane/persistence/repo"
)

// CredentialDto is the only shape a credential row may take crossing into
// an admin HTTP response — deliberately excludes tokenHash, userId,
// sessionId, and attachmentId, mirroring TS's AdminCredentialDto.
type CredentialDto struct {
	ID          string  `json:"id"`
	DeviceLabel string  `json:"deviceLabel"`
	Scope       string  `json:"scope"`
	NotBefore   *string `json:"notBefore"`
	ExpiresAt   *string `json:"expiresAt"`
	RevokedAt   *string `json:"revokedAt"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
}

func toCredentialDto(row repo.Credential) CredentialDto {
	return CredentialDto{
		ID:          row.ID,
		DeviceLabel: row.DeviceLabel,
		Scope:       row.Scope,
		NotBefore:   nullStringToPtr(row.NotBefore),
		ExpiresAt:   nullStringToPtr(row.ExpiresAt),
		RevokedAt:   nullStringToPtr(row.RevokedAt),
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

func nullStringToPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

// TeardownTarget identifies one attachment whose worker-side transport
// still needs tearing down after a DB-only admin mutation (ban,
// guest-revoke) committed. The adminapi HTTP handler drives that teardown
// via transport.Service.TeardownPendingAttachment, once per target.
type TeardownTarget struct {
	SessionID    string
	AttachmentID string
}
