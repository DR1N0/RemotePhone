// Package instance ports mako's TypeScript InstanceService
// (apps/controlplane/src/services/instance-service.ts) to Go: ownership-
// checked instance CRUD and start/stop/delete lifecycle against one
// configured worker. This is a faithful port with two deliberate
// deviations, both recorded in docs/architecture/decisions-log.md: (1) the
// one-instance-per-owner invariant is enforced by a real DB unique index
// (instances_unique_owner, migration 0011) instead of TS's check-then-
// insert-inside-an-immediate-transaction dance, which existed only to work
// around the absence of that index; (2) the stale `enrollmentRequired`
// check in assertCanCreateInstance is dropped, since that column no longer
// exists (migration 0010).
package instance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/persistence/repo"
	"mako/internal/controlplane/placement"
	workerapi "mako/internal/worker/api"
	"mako/internal/worker/runtime"

	"github.com/google/uuid"
)

const (
	actionInstanceStart  = "instance.start"
	actionInstanceStop   = "instance.stop"
	actionInstanceDelete = "instance.delete"
	targetTypeInstance   = "instance"
)

type Service struct {
	db                *sql.DB
	q                 *repo.Queries
	workerClient      WorkerClient
	placementSelector *placement.Selector
}

func NewService(db *sql.DB, workerClient WorkerClient, placementSelector *placement.Selector) *Service {
	return &Service{db: db, q: repo.New(db), workerClient: workerClient, placementSelector: placementSelector}
}

func (s *Service) withTx(ctx context.Context, fn func(q *repo.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(repo.New(tx)); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func toManagedInstance(row repo.Instance) (ManagedInstance, error) {
	var placementPolicy placement.Policy
	if err := json.Unmarshal([]byte(row.PlacementPolicyJson), &placementPolicy); err != nil {
		return ManagedInstance{}, fmt.Errorf("unmarshal placement_policy_json: %w", err)
	}
	var resourceProfile ResourceProfile
	if err := json.Unmarshal([]byte(row.ResourceProfileJson), &resourceProfile); err != nil {
		return ManagedInstance{}, fmt.Errorf("unmarshal resource_profile_json: %w", err)
	}
	var launchConfig runtime.LaunchConfig
	if err := json.Unmarshal([]byte(row.LaunchConfigJson), &launchConfig); err != nil {
		return ManagedInstance{}, fmt.Errorf("unmarshal launch_config_json: %w", err)
	}

	var handle *runtime.InstanceHandle
	if row.RuntimeHandleJson.Valid {
		var h runtime.InstanceHandle
		if err := json.Unmarshal([]byte(row.RuntimeHandleJson.String), &h); err != nil {
			return ManagedInstance{}, fmt.Errorf("unmarshal runtime_handle_json: %w", err)
		}
		handle = &h
	}

	return ManagedInstance{
		ID:              row.ID,
		Name:            row.Name,
		OwnerID:         row.OwnerID,
		RuntimeClass:    row.RuntimeClass,
		TemplateID:      row.TemplateID,
		Placement:       placementPolicy,
		ResourceProfile: resourceProfile,
		LaunchConfig:    launchConfig,
		DesiredState:    row.DesiredState,
		ObservedState:   row.ObservedState,
		RuntimeHandle:   handle,
		StateDataRef:    nullStringPtr(row.StateDataRef),
		LastError:       nullStringPtr(row.LastError),
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
	}, nil
}

func nullStringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func optionalNullString(v string) sql.NullString {
	if v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: v, Valid: true}
}

func (s *Service) getRequiredInstance(ctx context.Context, id string) (repo.Instance, error) {
	row, err := s.q.GetInstanceByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.Instance{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return repo.Instance{}, fmt.Errorf("get instance: %w", err)
	}
	return row, nil
}

func (s *Service) assertCanCreateInstance(ctx context.Context, ownerID string) error {
	owner, err := s.q.GetUserByID(ctx, ownerID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: user may not create an instance", ErrForbidden)
		}
		return fmt.Errorf("get owner: %w", err)
	}
	if owner.UserType == "guest" || owner.Status != "active" {
		return fmt.Errorf("%w: user may not create an instance", ErrForbidden)
	}
	return nil
}

func assertCanManageInstance(row repo.Instance, callerUserID string) error {
	if row.OwnerID != callerUserID {
		return fmt.Errorf("%w: user %s may not manage instance %s", ErrForbidden, callerUserID, row.ID)
	}
	return nil
}

func (s *Service) assertCanAdminManageInstance(ctx context.Context, callerUserID string) error {
	caller, err := s.q.GetUserByID(ctx, callerUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: user %s is not an active admin", ErrForbidden, callerUserID)
		}
		return fmt.Errorf("get caller: %w", err)
	}
	if caller.UserType != "admin" || caller.Status != "active" {
		return fmt.Errorf("%w: user %s is not an active admin", ErrForbidden, callerUserID)
	}
	return nil
}

func (s *Service) resolveTemplate(ctx context.Context, templateID string) (runtime.Template, error) {
	templates, err := s.workerClient.ListTemplates(ctx)
	if err != nil {
		return runtime.Template{}, fmt.Errorf("list templates: %w", err)
	}
	for _, t := range templates {
		if t.TemplateID == templateID {
			return t, nil
		}
	}
	return runtime.Template{}, fmt.Errorf("%w: runtime template not found: %s", ErrInvalidRequest, templateID)
}

// CreateManagedInstance validates req, resolves its template and placement
// policy against the one configured worker, and inserts the instance row.
// Unlike the TS version, there is no separate "validate placement, then
// insert in a transaction" step: Placement.Validate is a pure in-memory
// check (no workers table to race against — see decisions-log.md,
// 2026-10-14), and the one-instance-per-owner invariant is enforced by the
// instances_unique_owner DB index, so a single INSERT is already atomic.
func (s *Service) CreateManagedInstance(ctx context.Context, req CreateInstanceRequest, ownerID string) (ManagedInstance, error) {
	if err := req.Validate(); err != nil {
		return ManagedInstance{}, err
	}
	if err := s.assertCanCreateInstance(ctx, ownerID); err != nil {
		return ManagedInstance{}, err
	}
	if err := s.placementSelector.Validate(req.Placement); err != nil {
		return ManagedInstance{}, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	template, err := s.resolveTemplate(ctx, req.TemplateID)
	if err != nil {
		return ManagedInstance{}, err
	}

	placementJSON, err := json.Marshal(req.Placement)
	if err != nil {
		return ManagedInstance{}, fmt.Errorf("marshal placement: %w", err)
	}
	resourceJSON, err := json.Marshal(req.ResourceProfile)
	if err != nil {
		return ManagedInstance{}, fmt.Errorf("marshal resource profile: %w", err)
	}
	launchJSON, err := json.Marshal(req.LaunchConfig)
	if err != nil {
		return ManagedInstance{}, fmt.Errorf("marshal launch config: %w", err)
	}

	row, err := s.q.CreateInstance(ctx, repo.CreateInstanceParams{
		ID:                  uuid.NewString(),
		Name:                req.Name,
		RuntimeClass:        template.RuntimeClass,
		TemplateID:          template.TemplateID,
		PlacementPolicyJson: string(placementJSON),
		ResourceProfileJson: string(resourceJSON),
		LaunchConfigJson:    string(launchJSON),
		OwnerID:             ownerID,
	})
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return ManagedInstance{}, fmt.Errorf("%w: %s", ErrOwnerConflict, ownerID)
		}
		return ManagedInstance{}, fmt.Errorf("create instance: %w", err)
	}
	return toManagedInstance(row)
}

func (s *Service) ListManagedInstances(ctx context.Context, callerUserID string) ([]ManagedInstance, error) {
	rows, err := s.q.ListInstancesByOwner(ctx, callerUserID)
	if err != nil {
		return nil, fmt.Errorf("list instances: %w", err)
	}
	instances := make([]ManagedInstance, 0, len(rows))
	for _, row := range rows {
		mi, err := toManagedInstance(row)
		if err != nil {
			return nil, err
		}
		instances = append(instances, mi)
	}
	return instances, nil
}

func (s *Service) GetManagedInstance(ctx context.Context, id, callerUserID string) (ManagedInstance, error) {
	row, err := s.getRequiredInstance(ctx, id)
	if err != nil {
		return ManagedInstance{}, err
	}
	if err := assertCanManageInstance(row, callerUserID); err != nil {
		return ManagedInstance{}, err
	}
	return toManagedInstance(row)
}

// GetManagedInstanceUnchecked skips the owner-authorization check performed
// by GetManagedInstance — for internal, non-HTTP callers that already
// authorized the request at a higher layer (Phase 2's transport package,
// looking up the instance an already-authorized session belongs to).
func (s *Service) GetManagedInstanceUnchecked(ctx context.Context, id string) (ManagedInstance, error) {
	row, err := s.getRequiredInstance(ctx, id)
	if err != nil {
		return ManagedInstance{}, err
	}
	return toManagedInstance(row)
}

func (s *Service) StartManagedInstance(ctx context.Context, id, callerUserID string, actor audit.Context) (ManagedInstance, error) {
	row, err := s.getRequiredInstance(ctx, id)
	if err != nil {
		return ManagedInstance{}, err
	}
	if err := assertCanManageInstance(row, callerUserID); err != nil {
		return ManagedInstance{}, err
	}
	return s.startInstanceLifecycle(ctx, row, actor)
}

func (s *Service) StopManagedInstance(ctx context.Context, id, callerUserID string, actor audit.Context) (ManagedInstance, error) {
	row, err := s.getRequiredInstance(ctx, id)
	if err != nil {
		return ManagedInstance{}, err
	}
	if err := assertCanManageInstance(row, callerUserID); err != nil {
		return ManagedInstance{}, err
	}
	return s.stopInstanceLifecycle(ctx, row, actor)
}

func (s *Service) DeleteManagedInstance(ctx context.Context, id, callerUserID string, actor audit.Context) error {
	row, err := s.getRequiredInstance(ctx, id)
	if err != nil {
		return err
	}
	if err := assertCanManageInstance(row, callerUserID); err != nil {
		return err
	}
	return s.deleteInstanceLifecycle(ctx, row, actor)
}

func (s *Service) StartAdminManagedInstance(ctx context.Context, id, callerUserID string, actor audit.Context) (ManagedInstance, error) {
	if err := s.assertCanAdminManageInstance(ctx, callerUserID); err != nil {
		return ManagedInstance{}, err
	}
	row, err := s.getRequiredInstance(ctx, id)
	if err != nil {
		return ManagedInstance{}, err
	}
	return s.startInstanceLifecycle(ctx, row, actor)
}

func (s *Service) StopAdminManagedInstance(ctx context.Context, id, callerUserID string, actor audit.Context) (ManagedInstance, error) {
	if err := s.assertCanAdminManageInstance(ctx, callerUserID); err != nil {
		return ManagedInstance{}, err
	}
	row, err := s.getRequiredInstance(ctx, id)
	if err != nil {
		return ManagedInstance{}, err
	}
	return s.stopInstanceLifecycle(ctx, row, actor)
}

func (s *Service) DeleteAdminManagedInstance(ctx context.Context, id, callerUserID string, actor audit.Context) error {
	if err := s.assertCanAdminManageInstance(ctx, callerUserID); err != nil {
		return err
	}
	row, err := s.getRequiredInstance(ctx, id)
	if err != nil {
		return err
	}
	return s.deleteInstanceLifecycle(ctx, row, actor)
}

// recordTerminalPersistenceFailure is called when a worker lifecycle call
// already succeeded but persisting that outcome failed. It makes a
// best-effort attempt to at least log a failure audit event (its own,
// fresh query — the original transaction already rolled back); if even
// that fails, the caller gets ErrAuditTerminalOutcomeUnavailable instead of
// persistenceErr, so a worker/DB divergence with zero audit trail is never
// mistaken for an ordinary error.
func (s *Service) recordTerminalPersistenceFailure(ctx context.Context, actor audit.Context, action, targetID string, persistenceErr error) error {
	if auditErr := audit.Record(ctx, s.q, actor, action, targetTypeInstance, targetID, audit.OutcomeFailure); auditErr != nil {
		return &ErrAuditTerminalOutcomeUnavailable{PersistenceErr: persistenceErr, AuditErr: auditErr}
	}
	return persistenceErr
}

func (s *Service) startInstanceLifecycle(ctx context.Context, row repo.Instance, actor audit.Context) (ManagedInstance, error) {
	if err := audit.Record(ctx, s.q, actor, actionInstanceStart, targetTypeInstance, row.ID, audit.OutcomeAttempt); err != nil {
		return ManagedInstance{}, fmt.Errorf("record attempt audit event: %w", err)
	}

	var launchConfig runtime.LaunchConfig
	if err := json.Unmarshal([]byte(row.LaunchConfigJson), &launchConfig); err != nil {
		return ManagedInstance{}, fmt.Errorf("unmarshal launch_config_json: %w", err)
	}

	response, err := s.workerClient.StartInstance(ctx, workerapi.StartInstanceRequest{
		InstanceID:   row.ID,
		RuntimeClass: row.RuntimeClass,
		TemplateID:   row.TemplateID,
		LaunchConfig: launchConfig,
	})
	if err != nil {
		if auditErr := audit.Record(ctx, s.q, actor, actionInstanceStart, targetTypeInstance, row.ID, audit.OutcomeFailure); auditErr != nil {
			return ManagedInstance{}, auditErr
		}
		return ManagedInstance{}, fmt.Errorf("start instance on worker: %w", err)
	}

	observedState := response.State
	if observedState == "" {
		observedState = "starting"
	}
	handleJSON, err := json.Marshal(response.Handle)
	if err != nil {
		return ManagedInstance{}, fmt.Errorf("marshal runtime handle: %w", err)
	}

	var result ManagedInstance
	txErr := s.withTx(ctx, func(q *repo.Queries) error {
		if err := q.UpdateInstanceLifecycle(ctx, repo.UpdateInstanceLifecycleParams{
			DesiredState:      "running",
			ObservedState:     observedState,
			RuntimeHandleJson: sql.NullString{String: string(handleJSON), Valid: true},
			ID:                row.ID,
		}); err != nil {
			return fmt.Errorf("update instance lifecycle: %w", err)
		}
		if err := audit.Record(ctx, q, actor, actionInstanceStart, targetTypeInstance, row.ID, audit.OutcomeSuccess); err != nil {
			return err
		}
		updated, err := q.GetInstanceByID(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("reload instance: %w", err)
		}
		result, err = toManagedInstance(updated)
		return err
	})
	if txErr != nil {
		return ManagedInstance{}, s.recordTerminalPersistenceFailure(ctx, actor, actionInstanceStart, row.ID, txErr)
	}
	return result, nil
}

func (s *Service) stopInstanceLifecycle(ctx context.Context, row repo.Instance, actor audit.Context) (ManagedInstance, error) {
	if !row.RuntimeHandleJson.Valid {
		var result ManagedInstance
		txErr := s.withTx(ctx, func(q *repo.Queries) error {
			if err := q.UpdateInstanceLifecycle(ctx, repo.UpdateInstanceLifecycleParams{
				DesiredState:  "stopped",
				ObservedState: "stopped",
				ID:            row.ID,
			}); err != nil {
				return fmt.Errorf("update instance lifecycle: %w", err)
			}
			if err := audit.Record(ctx, q, actor, actionInstanceStop, targetTypeInstance, row.ID, audit.OutcomeSuccess); err != nil {
				return err
			}
			updated, err := q.GetInstanceByID(ctx, row.ID)
			if err != nil {
				return fmt.Errorf("reload instance: %w", err)
			}
			result, err = toManagedInstance(updated)
			return err
		})
		if txErr != nil {
			return ManagedInstance{}, s.recordTerminalPersistenceFailure(ctx, actor, actionInstanceStop, row.ID, txErr)
		}
		return result, nil
	}

	if err := audit.Record(ctx, s.q, actor, actionInstanceStop, targetTypeInstance, row.ID, audit.OutcomeAttempt); err != nil {
		return ManagedInstance{}, fmt.Errorf("record attempt audit event: %w", err)
	}

	response, err := s.workerClient.StopInstance(ctx, row.ID)
	if err != nil {
		if auditErr := audit.Record(ctx, s.q, actor, actionInstanceStop, targetTypeInstance, row.ID, audit.OutcomeFailure); auditErr != nil {
			return ManagedInstance{}, auditErr
		}
		return ManagedInstance{}, fmt.Errorf("stop instance on worker: %w", err)
	}

	observedState := response.State
	if observedState == "" {
		observedState = "stopped"
	}

	var result ManagedInstance
	txErr := s.withTx(ctx, func(q *repo.Queries) error {
		if err := q.UpdateInstanceLifecycle(ctx, repo.UpdateInstanceLifecycleParams{
			DesiredState:  "stopped",
			ObservedState: observedState,
			LastError:     optionalNullString(response.LastError),
			ID:            row.ID,
		}); err != nil {
			return fmt.Errorf("update instance lifecycle: %w", err)
		}
		if err := audit.Record(ctx, q, actor, actionInstanceStop, targetTypeInstance, row.ID, audit.OutcomeSuccess); err != nil {
			return err
		}
		updated, err := q.GetInstanceByID(ctx, row.ID)
		if err != nil {
			return fmt.Errorf("reload instance: %w", err)
		}
		result, err = toManagedInstance(updated)
		return err
	})
	if txErr != nil {
		return ManagedInstance{}, s.recordTerminalPersistenceFailure(ctx, actor, actionInstanceStop, row.ID, txErr)
	}
	return result, nil
}

func (s *Service) deleteInstanceLifecycle(ctx context.Context, row repo.Instance, actor audit.Context) error {
	return s.withTx(ctx, func(q *repo.Queries) error {
		if err := q.DeleteInstanceByID(ctx, row.ID); err != nil {
			return fmt.Errorf("delete instance: %w", err)
		}
		return audit.Record(ctx, q, actor, actionInstanceDelete, targetTypeInstance, row.ID, audit.OutcomeSuccess)
	})
}

func isUniqueConstraintViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
