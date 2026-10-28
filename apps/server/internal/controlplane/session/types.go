package session

import (
	"sort"

	"remotephone/internal/controlplane/persistence/repo"
)

// Role order for sortAttachments: owner first, then participant, then
// observer, matching session-service.ts's roleOrder table.
var roleOrder = map[string]int{"owner": 0, "participant": 1, "observer": 2}

type AttachmentRecord struct {
	ID                       string  `json:"id"`
	SessionID                string  `json:"sessionId"`
	ClientID                 string  `json:"clientId"`
	UserID                   *string `json:"userId"`
	Role                     string  `json:"role"`
	PendingTargetRole        *string `json:"pendingTargetRole"`
	RequestedTransportRole   string  `json:"requestedTransportRole"`
	EffectiveTransportRole   string  `json:"effectiveTransportRole"`
	Status                   string  `json:"status"`
	TransportTeardownPending bool    `json:"transportTeardownPending"`
	CreatedAt                string  `json:"createdAt"`
	UpdatedAt                string  `json:"updatedAt"`
}

type SessionRecord struct {
	ID                   string             `json:"id"`
	InstanceID           string             `json:"instanceId"`
	Status               string             `json:"status"`
	OwnerAbsenceDeadline *string            `json:"ownerAbsenceDeadline"`
	Attachments          []AttachmentRecord `json:"attachments"`
	CreatedAt            string             `json:"createdAt"`
	UpdatedAt            string             `json:"updatedAt"`
}

// EffectiveTransportRole folds a pending, unconfirmed role transition to
// "observer" regardless of direction (promoting or demoting) — the single
// pending_target_role column (migration 0012 dropped the redundant
// restrictive_pending_role) is the sole source of this fail-safe. Exported
// for the transport package, which needs the same computation when pushing
// a role to the worker.
func EffectiveTransportRole(role string, pendingTargetRole *string) string {
	if pendingTargetRole != nil {
		return "observer"
	}
	return role
}

// toAttachmentRecord is the one shared mapper every attachment-producing
// method in this package uses — TS duplicated this function near-verbatim
// between session-service.ts and session-invite-service.ts.
func toAttachmentRecord(row repo.Attachment) AttachmentRecord {
	var pendingTargetRole *string
	if row.PendingTargetRole.Valid {
		v := row.PendingTargetRole.String
		pendingTargetRole = &v
	}
	requestedTransportRole := row.Role
	if pendingTargetRole != nil {
		requestedTransportRole = *pendingTargetRole
	}

	var userID *string
	if row.UserID.Valid {
		v := row.UserID.String
		userID = &v
	}

	return AttachmentRecord{
		ID:                       row.ID,
		SessionID:                row.SessionID,
		ClientID:                 row.ClientID,
		UserID:                   userID,
		Role:                     row.Role,
		PendingTargetRole:        pendingTargetRole,
		RequestedTransportRole:   requestedTransportRole,
		EffectiveTransportRole:   EffectiveTransportRole(row.Role, pendingTargetRole),
		Status:                   row.Status,
		TransportTeardownPending: row.TransportTeardownPending != 0,
		CreatedAt:                row.CreatedAt,
		UpdatedAt:                row.UpdatedAt,
	}
}

func sortAttachments(rows []repo.Attachment) []repo.Attachment {
	sorted := make([]repo.Attachment, len(rows))
	copy(sorted, rows)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := sorted[i], sorted[j]
		if roleOrder[left.Role] != roleOrder[right.Role] {
			return roleOrder[left.Role] < roleOrder[right.Role]
		}
		if left.CreatedAt != right.CreatedAt {
			return left.CreatedAt < right.CreatedAt
		}
		return left.UpdatedAt < right.UpdatedAt
	})
	return sorted
}

func toSessionRecord(row repo.Session, attachmentRows []repo.Attachment) SessionRecord {
	var ownerAbsenceDeadline *string
	if row.OwnerAbsenceDeadline.Valid {
		v := row.OwnerAbsenceDeadline.String
		ownerAbsenceDeadline = &v
	}

	sorted := sortAttachments(attachmentRows)
	attachments := make([]AttachmentRecord, 0, len(sorted))
	for _, a := range sorted {
		attachments = append(attachments, toAttachmentRecord(a))
	}

	return SessionRecord{
		ID:                   row.ID,
		InstanceID:           row.InstanceID,
		Status:               row.Status,
		OwnerAbsenceDeadline: ownerAbsenceDeadline,
		Attachments:          attachments,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}
}
