// Package instance ports remotephone's pre-Go TypeScript InstanceService to Go:
// ownership-
// checked instance CRUD and start/stop/delete lifecycle against one
// configured worker. Unlike the original TS port, a user may own more than
// one instance (migration 0014 removed the one-per-owner unique index), so
// there is no create-time conflict to map. The stale `enrollmentRequired`
// check in assertCanCreateInstance is dropped, since that column no longer
// exists (migration 0010).
package instance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"remotephone/internal/controlplane/audit"
	"remotephone/internal/controlplane/persistence/repo"
	"remotephone/internal/controlplane/placement"
	workerapi "remotephone/internal/worker/api"
	"remotephone/internal/worker/runtime"

	"github.com/google/uuid"
)

const (
	actionInstanceStart         = "instance.start"
	actionInstanceStop          = "instance.stop"
	actionInstanceDelete        = "instance.delete"
	actionInstanceWorkerRespawn = "instance.worker.respawn"
	targetTypeInstance          = "instance"
)

// PlacementSelector is the subset of placement.Selector's behavior
// instance.Service needs: validating a requested placement policy, and
// acquiring/releasing the one worker process a running instance (or a
// short-lived template-discovery lookup, see resolveTemplate) gets. A
// narrow interface — not the concrete *placement.Selector — so this
// package's own tests can fake process spawning entirely, never shelling
// out to a real binary (see docs/architecture/decisions-log.md's testing
// bar for this phase).
type PlacementSelector interface {
	Validate(policy placement.Policy) error
	AcquireWorker(ctx context.Context, instanceID string) (baseURL string, pid int, err error)
	ReleaseWorker(ctx context.Context, pid int) error
}

// BaseImageResolver is the subset of image.Service's behavior
// startInstanceLifecycle needs — its own narrow interface (rather than a
// direct *image.Service field) both for testability and because
// image.Service's own capture flow needs to read the instances table
// directly instead of depending on this package, to avoid an import cycle.
type BaseImageResolver interface {
	ResolveCurrentBaseImage(ctx context.Context, templateID string) (artifactPath string, ok bool, err error)
}

type Service struct {
	db                *sql.DB
	q                 *repo.Queries
	placementSelector PlacementSelector
	imageSvc          BaseImageResolver
}

// NewService constructs a Service. Unlike pre-Phase-4, there is no single
// shared WorkerClient: every worker interaction resolves a client scoped to
// one specific spawned worker process, via resolveTemplate (a short-lived
// discovery worker) or WorkerClientFor (a running instance's own worker,
// from its persisted RuntimeHandle.WorkerBaseURL). See
// docs/plans/worker-lifecycle.md.
func NewService(db *sql.DB, placementSelector PlacementSelector) *Service {
	return &Service{db: db, q: repo.New(db), placementSelector: placementSelector}
}

// SetBaseImageResolver wires in the image package's base-image resolution
// — optional and settable after construction (rather than a NewService
// param) so the ten-plus existing call sites that don't care about this
// feature are unaffected. A nil imageSvc (the default) means "no base image
// has ever been promoted for any template," i.e. every instance clones from
// its template's live, on-disk AVD exactly as before this feature existed.
func (s *Service) SetBaseImageResolver(imageSvc BaseImageResolver) {
	s.imageSvc = imageSvc
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
	if (caller.UserType != "admin" && caller.UserType != "system") || caller.Status != "active" {
		return fmt.Errorf("%w: user %s is not an active admin", ErrForbidden, callerUserID)
	}
	return nil
}

// resolveTemplate discovers templates via a short-lived, discardable worker
// process. Template/runtime discovery is host-level metadata — identical
// regardless of which worker answers — not tied to any specific instance,
// so there's no "the instance's worker" to ask yet at creation time (no
// instance row, and no worker, exist before this call returns). Spawn, ask,
// release: symmetric with the per-running-instance lifecycle below, no
// persistent discovery-only worker process to special-case.
func (s *Service) resolveTemplate(ctx context.Context, templateID string) (runtime.Template, error) {
	client, release, err := s.acquireDiscoveryWorker(ctx)
	if err != nil {
		return runtime.Template{}, err
	}
	defer release()

	templates, err := client.ListTemplates(ctx)
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

// ListTemplates discovers the host's available runtime templates for the
// client template picker. It spawns a short-lived discovery worker exactly
// like resolveTemplate, but returns the client-safe subset (no host paths
// or other Metadata) — the control plane never exposes worker-local filesystem
// details to a client.
func (s *Service) ListTemplates(ctx context.Context) ([]TemplateInfo, error) {
	client, release, err := s.acquireDiscoveryWorker(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	templates, err := client.ListTemplates(ctx)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	result := make([]TemplateInfo, 0, len(templates))
	for _, t := range templates {
		result = append(result, TemplateInfo{
			TemplateID:   t.TemplateID,
			DisplayName:  t.DisplayName,
			RuntimeClass: t.RuntimeClass,
		})
	}
	return result, nil
}

func (s *Service) acquireDiscoveryWorker(ctx context.Context) (WorkerClient, func(), error) {
	baseURL, pid, err := s.placementSelector.AcquireWorker(ctx, "discovery-"+uuid.NewString())
	if err != nil {
		return nil, nil, fmt.Errorf("acquire discovery worker: %w", err)
	}
	release := func() {
		// context.Background(), not ctx: cleanup must run even if the
		// request that triggered it was itself canceled.
		_ = s.placementSelector.ReleaseWorker(context.Background(), pid)
	}
	return NewHTTPWorkerClient(baseURL), release, nil
}

// WorkerClientFor resolves a WorkerClient for id's currently-serving worker
// process, from its persisted RuntimeHandle.WorkerBaseURL. Exported so
// transport.Service — which talks to the same per-instance worker for
// transport-session/signaling calls — uses this instead of holding its own
// client (see docs/plans/worker-lifecycle.md).
func (s *Service) WorkerClientFor(ctx context.Context, id string) (WorkerClient, error) {
	row, err := s.getRequiredInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	_, client, err := s.runningWorker(row)
	return client, err
}

// runningWorker unmarshals row's persisted handle and returns both it and a
// WorkerClient for its WorkerBaseURL. Errors (as ErrInvalidRequest) if the
// instance has never been started or has no worker recorded.
func (s *Service) runningWorker(row repo.Instance) (runtime.InstanceHandle, WorkerClient, error) {
	if !row.RuntimeHandleJson.Valid {
		return runtime.InstanceHandle{}, nil, fmt.Errorf("%w: instance %s has no running worker", ErrInvalidRequest, row.ID)
	}
	var handle runtime.InstanceHandle
	if err := json.Unmarshal([]byte(row.RuntimeHandleJson.String), &handle); err != nil {
		return runtime.InstanceHandle{}, nil, fmt.Errorf("unmarshal runtime_handle_json: %w", err)
	}
	if handle.WorkerBaseURL == "" {
		return runtime.InstanceHandle{}, nil, fmt.Errorf("%w: instance %s has no worker base url recorded", ErrInvalidRequest, row.ID)
	}
	return handle, NewHTTPWorkerClient(handle.WorkerBaseURL), nil
}

// CreateManagedInstance validates req, resolves its template and placement
// policy against the one configured worker, and inserts the instance row.
// A user may create any number of instances (each becomes its own AVD and
// its own worker process when started) — the original one-per-owner unique
// index was removed by migration 0014. Placement.Validate is a pure
// in-memory check (no workers table to race against — see decisions-log.md,
// 2026-10-14), so a single INSERT remains atomic.
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

// ListAllInstances returns every instance across all owners — the
// cross-owner sibling of ListManagedInstances, for admin's instance-listing
// route. Callers must have already authorized the caller as an admin.
func (s *Service) ListAllInstances(ctx context.Context) ([]ManagedInstance, error) {
	rows, err := s.q.ListInstances(ctx)
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

// ListRunningInstanceIDs returns the IDs of every instance whose desired
// state is "running" — candidate discovery for the controller's
// worker-liveness resync ticker, same shape as session's
// ListExpiredOwnerAbsenceSessionIDs.
func (s *Service) ListRunningInstanceIDs(ctx context.Context) ([]string, error) {
	rows, err := s.q.ListInstancesByDesiredState(ctx, "running")
	if err != nil {
		return nil, fmt.Errorf("list instances by desired state: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

// UpdateRuntimeHandle persists handle as id's current runtime handle,
// leaving desired/observed state untouched — for the worker-liveness
// Reconciler (internal/controlplane/reconcile) to record a respawned
// worker's new PID/base URL after adopting the still-running emulator,
// without going through the full start/stop lifecycle (no worker HTTP call
// belongs here, the Reconciler already made it).
func (s *Service) UpdateRuntimeHandle(ctx context.Context, id string, handle runtime.InstanceHandle, actor audit.Context) (ManagedInstance, error) {
	handleJSON, err := json.Marshal(handle)
	if err != nil {
		return ManagedInstance{}, fmt.Errorf("marshal runtime handle: %w", err)
	}
	var result ManagedInstance
	err = s.withTx(ctx, func(q *repo.Queries) error {
		row, err := q.GetInstanceByID(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrNotFound, id)
			}
			return fmt.Errorf("get instance: %w", err)
		}
		if err := q.UpdateInstanceRuntime(ctx, repo.UpdateInstanceRuntimeParams{
			ObservedState:     row.ObservedState,
			RuntimeHandleJson: sql.NullString{String: string(handleJSON), Valid: true},
			LastError:         row.LastError,
			ID:                id,
		}); err != nil {
			return fmt.Errorf("update instance runtime: %w", err)
		}
		if err := audit.Record(ctx, q, actor, actionInstanceWorkerRespawn, targetTypeInstance, id, audit.OutcomeSuccess); err != nil {
			return err
		}
		updated, err := q.GetInstanceByID(ctx, id)
		if err != nil {
			return fmt.Errorf("reload instance: %w", err)
		}
		result, err = toManagedInstance(updated)
		return err
	})
	if err != nil {
		return ManagedInstance{}, err
	}
	return result, nil
}

func nullStringFromPtr(v *string) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *v, Valid: true}
}

// UpdateObservedState persists a freshly-observed state for id, leaving
// desired_state and the runtime handle untouched — the counterpart to
// UpdateRuntimeHandle (which does the reverse: touches the handle, leaves
// observed_state as-is). Used by WorkerLivenessProbe to sync observed_state
// once a worker reports the runtime has actually come up, since
// StartManagedInstance itself only ever writes "starting" (the worker
// hasn't finished launching yet at that point) and nothing updates it
// afterward otherwise — not routed through audit.Record: this is routine
// reconciliation sync, not an administrative action, same reasoning as
// WorkerLivenessProbe's own healthy-no-op path going unaudited.
func (s *Service) UpdateObservedState(ctx context.Context, id, observedState string, lastError *string) (ManagedInstance, error) {
	var result ManagedInstance
	err := s.withTx(ctx, func(q *repo.Queries) error {
		row, err := q.GetInstanceByID(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrNotFound, id)
			}
			return fmt.Errorf("get instance: %w", err)
		}
		if err := q.UpdateInstanceRuntime(ctx, repo.UpdateInstanceRuntimeParams{
			ObservedState:     observedState,
			RuntimeHandleJson: row.RuntimeHandleJson,
			LastError:         nullStringFromPtr(lastError),
			ID:                id,
		}); err != nil {
			return fmt.Errorf("update instance runtime: %w", err)
		}
		updated, err := q.GetInstanceByID(ctx, id)
		if err != nil {
			return fmt.Errorf("reload instance: %w", err)
		}
		result, err = toManagedInstance(updated)
		return err
	})
	if err != nil {
		return ManagedInstance{}, err
	}
	return result, nil
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

	baseURL, pid, err := s.placementSelector.AcquireWorker(ctx, row.ID)
	if err != nil {
		if auditErr := audit.Record(ctx, s.q, actor, actionInstanceStart, targetTypeInstance, row.ID, audit.OutcomeFailure); auditErr != nil {
			return ManagedInstance{}, auditErr
		}
		return ManagedInstance{}, fmt.Errorf("acquire worker: %w", err)
	}
	workerClient := NewHTTPWorkerClient(baseURL)

	// Best-effort: a resolution failure here must never block starting the
	// instance over what's purely an optimization — it just falls back to
	// an empty path, which the worker already treats as "no promoted base
	// image, clone from the live AVD" (today's original behavior).
	var baseImageArtifactPath string
	if s.imageSvc != nil {
		if path, ok, err := s.imageSvc.ResolveCurrentBaseImage(ctx, row.TemplateID); err == nil && ok {
			baseImageArtifactPath = path
		}
	}

	response, err := workerClient.StartInstance(ctx, workerapi.StartInstanceRequest{
		InstanceID:            row.ID,
		RuntimeClass:          row.RuntimeClass,
		TemplateID:            row.TemplateID,
		LaunchConfig:          launchConfig,
		BaseImageArtifactPath: baseImageArtifactPath,
	})
	if err != nil {
		_ = s.placementSelector.ReleaseWorker(context.Background(), pid)
		if auditErr := audit.Record(ctx, s.q, actor, actionInstanceStart, targetTypeInstance, row.ID, audit.OutcomeFailure); auditErr != nil {
			return ManagedInstance{}, auditErr
		}
		return ManagedInstance{}, fmt.Errorf("start instance on worker: %w", err)
	}

	observedState := response.State
	if observedState == "" {
		observedState = "starting"
	}
	// WorkerPID/WorkerBaseURL record the worker OS process the control
	// plane just spawned for this instance — independent of
	// response.Handle's emulator-process fields, which the worker itself
	// filled in.
	response.Handle.WorkerPID = pid
	response.Handle.WorkerBaseURL = baseURL
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
			StateDataRef:      optionalNullString(response.Handle.DataDir),
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
				StateDataRef:  row.StateDataRef,
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

	handle, workerClient, err := s.runningWorker(row)
	if err != nil {
		return ManagedInstance{}, err
	}

	if err := audit.Record(ctx, s.q, actor, actionInstanceStop, targetTypeInstance, row.ID, audit.OutcomeAttempt); err != nil {
		return ManagedInstance{}, fmt.Errorf("record attempt audit event: %w", err)
	}

	response, err := workerClient.StopInstance(ctx, row.ID)
	if err != nil {
		if auditErr := audit.Record(ctx, s.q, actor, actionInstanceStop, targetTypeInstance, row.ID, audit.OutcomeFailure); auditErr != nil {
			return ManagedInstance{}, auditErr
		}
		return ManagedInstance{}, fmt.Errorf("stop instance on worker: %w", err)
	}

	// The worker's job for this instance is done now that the emulator has
	// been told to stop — release its process. Best-effort: a release
	// failure here doesn't block recording the instance stopped (the
	// emulator already is), same "don't compound a success with a harder
	// failure" philosophy as recordTerminalPersistenceFailure below.
	if handle.WorkerPID != 0 {
		_ = s.placementSelector.ReleaseWorker(ctx, handle.WorkerPID)
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
			StateDataRef:  row.StateDataRef,
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

// deleteInstanceLifecycle deletes row regardless of its desired state —
// matching the TS behavior this ports, no new "must be stopped first"
// requirement. Phase 4 does add one thing: a running instance's worker OS
// process is released (best-effort) before the row disappears, so deleting
// a running instance can no longer leak a worker process forever the way it
// harmlessly used to when every instance shared one externally-managed
// worker.
func (s *Service) deleteInstanceLifecycle(ctx context.Context, row repo.Instance, actor audit.Context) error {
	if handle, _, err := s.runningWorker(row); err == nil && handle.WorkerPID != 0 {
		_ = s.placementSelector.ReleaseWorker(ctx, handle.WorkerPID)
	}
	if row.StateDataRef.Valid {
		// Best-effort, same philosophy as ReleaseWorker above: a cleanup
		// failure here must never block the instance row itself from being
		// deleted — worst case is an orphaned AVD directory taking up disk,
		// not a correctness problem. By the time an instance is usually
		// deleted its own worker process is long gone (stopped earlier), so
		// this spins up a short-lived worker just for this call, mirroring
		// resolveTemplate's discovery-worker pattern.
		if client, release, err := s.acquireDiscoveryWorker(ctx); err == nil {
			_ = client.CleanupInstanceData(ctx, row.ID, row.RuntimeClass)
			release()
		}
	}
	return s.withTx(ctx, func(q *repo.Queries) error {
		if err := q.DeleteInstanceByID(ctx, row.ID); err != nil {
			return fmt.Errorf("delete instance: %w", err)
		}
		return audit.Record(ctx, q, actor, actionInstanceDelete, targetTypeInstance, row.ID, audit.OutcomeSuccess)
	})
}
