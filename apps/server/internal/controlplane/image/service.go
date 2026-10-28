package image

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"remotephone/internal/controlplane/audit"
	"remotephone/internal/controlplane/persistence/repo"
	workerapi "remotephone/internal/worker/api"

	"github.com/google/uuid"
)

const (
	actionImageCapture  = "image.capture"
	actionImagePromote  = "image.promote"
	targetTypeBaseImage = "base_image"
	targetTypeTemplate  = "template"
)

// PlacementSelector is the subset of placement.Selector's behavior this
// package needs — deliberately its own narrow interface (not
// instance.PlacementSelector) so this package has no dependency on the
// instance package at all: instance.Service needs to call into this
// package at instance-start time (ResolveCurrentBaseImage), and Go doesn't
// allow import cycles, so the dependency can only go one way.
type PlacementSelector interface {
	AcquireWorker(ctx context.Context, instanceID string) (baseURL string, pid int, err error)
	ReleaseWorker(ctx context.Context, pid int) error
}

// Service implements capture/promote orchestration. It reads the instances
// table directly (rather than depending on instance.Service) for the same
// import-cycle reason as PlacementSelector above.
type Service struct {
	db                *sql.DB
	q                 *repo.Queries
	placementSelector PlacementSelector
	httpClient        *http.Client
}

func NewService(db *sql.DB, placementSelector PlacementSelector) *Service {
	return &Service{db: db, q: repo.New(db), placementSelector: placementSelector, httpClient: &http.Client{}}
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

func (s *Service) assertCanAdminManage(ctx context.Context, callerUserID string) error {
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

func isUniqueConstraintViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// captureOnWorker calls a short-lived discovery worker's capture endpoint
// directly over HTTP — a minimal, purpose-built client rather than reusing
// instance.HTTPWorkerClient, to keep this package's only dependency on the
// worker a wire-format one (workerapi's request/response structs), not a
// package (instance) that itself needs to depend on this one.
func (s *Service) captureOnWorker(ctx context.Context, workerBaseURL string, req workerapi.CaptureBaseImageRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal capture request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, workerBaseURL+"/v1/base-images/capture", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("capture base image on worker: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("capture base image on worker: status %d", resp.StatusCode)
	}
	var out workerapi.CaptureBaseImageResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode capture response: %w", err)
	}
	return out.ArtifactPath, nil
}

func toBaseImage(row repo.BaseImage) BaseImage {
	return BaseImage{
		ID:               row.ID,
		TemplateID:       row.TemplateID,
		SourceInstanceID: row.SourceInstanceID,
		ArtifactPath:     row.ArtifactPath,
		CreatedByUserID:  row.CreatedByUserID,
		CreatedAt:        row.CreatedAt,
	}
}

func toTemplate(row repo.Template) Template {
	t := Template{
		ID:           row.ID,
		RuntimeClass: row.RuntimeClass,
		DisplayName:  row.DisplayName,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}
	if row.CurrentBaseImageID.Valid {
		t.CurrentBaseImageID = &row.CurrentBaseImageID.String
	}
	return t
}

// CaptureCandidateImage snapshots instanceID's current AVD state into a new,
// immutable, inert base_images row — it never affects what any other
// instance resolves to (only PromoteBaseImage does that). Owner-scoped:
// callerUserID must own instanceID.
func (s *Service) CaptureCandidateImage(ctx context.Context, instanceID, callerUserID string, actor audit.Context) (BaseImage, error) {
	row, err := s.q.GetInstanceByID(ctx, instanceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BaseImage{}, fmt.Errorf("%w: instance %s", ErrNotFound, instanceID)
		}
		return BaseImage{}, fmt.Errorf("get instance: %w", err)
	}
	if row.OwnerID != callerUserID {
		return BaseImage{}, fmt.Errorf("%w: user %s may not capture an image of instance %s", ErrForbidden, callerUserID, instanceID)
	}
	if row.DesiredState != "stopped" {
		return BaseImage{}, fmt.Errorf("%w: instance must be stopped before capturing an image", ErrInvalidRequest)
	}
	if !row.StateDataRef.Valid {
		return BaseImage{}, fmt.Errorf("%w: instance has never been started, nothing to capture", ErrInvalidRequest)
	}

	baseImageID := uuid.NewString()
	workerBaseURL, pid, err := s.placementSelector.AcquireWorker(ctx, "discovery-"+uuid.NewString())
	if err != nil {
		return BaseImage{}, fmt.Errorf("acquire discovery worker: %w", err)
	}
	defer func() { _ = s.placementSelector.ReleaseWorker(context.Background(), pid) }()

	artifactPath, err := s.captureOnWorker(ctx, workerBaseURL, workerapi.CaptureBaseImageRequest{
		RuntimeClass:  row.RuntimeClass,
		BaseImageID:   baseImageID,
		SourceDataDir: row.StateDataRef.String,
	})
	if err != nil {
		return BaseImage{}, err
	}

	var result BaseImage
	txErr := s.withTx(ctx, func(q *repo.Queries) error {
		if _, err := q.CreateTemplate(ctx, repo.CreateTemplateParams{
			ID:           row.TemplateID,
			RuntimeClass: row.RuntimeClass,
			DisplayName:  row.TemplateID,
		}); err != nil && !isUniqueConstraintViolation(err) {
			return fmt.Errorf("ensure template row: %w", err)
		}

		created, err := q.CreateBaseImage(ctx, repo.CreateBaseImageParams{
			ID:               baseImageID,
			TemplateID:       row.TemplateID,
			SourceInstanceID: instanceID,
			ArtifactPath:     artifactPath,
			CreatedByUserID:  callerUserID,
		})
		if err != nil {
			return fmt.Errorf("create base image: %w", err)
		}

		if err := audit.Record(ctx, q, actor, actionImageCapture, targetTypeBaseImage, created.ID, audit.OutcomeSuccess); err != nil {
			return err
		}
		result = toBaseImage(created)
		return nil
	})
	if txErr != nil {
		return BaseImage{}, txErr
	}
	return result, nil
}

// PromoteBaseImage moves candidateID's template's "current" pointer to
// candidateID — the only operation in this package that changes what any
// other instance resolves to. Admin-only. Rollback is the same call with an
// older candidate's id; no separate code path.
func (s *Service) PromoteBaseImage(ctx context.Context, candidateID, callerUserID string, actor audit.Context) (Template, error) {
	if err := s.assertCanAdminManage(ctx, callerUserID); err != nil {
		return Template{}, err
	}

	baseImg, err := s.q.GetBaseImageByID(ctx, candidateID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Template{}, fmt.Errorf("%w: base image %s", ErrNotFound, candidateID)
		}
		return Template{}, fmt.Errorf("get base image: %w", err)
	}

	var result Template
	txErr := s.withTx(ctx, func(q *repo.Queries) error {
		if err := q.UpdateTemplateCurrentBaseImage(ctx, repo.UpdateTemplateCurrentBaseImageParams{
			CurrentBaseImageID: sql.NullString{String: candidateID, Valid: true},
			ID:                 baseImg.TemplateID,
		}); err != nil {
			return fmt.Errorf("update template current base image: %w", err)
		}

		if err := audit.Record(ctx, q, actor, actionImagePromote, targetTypeTemplate, baseImg.TemplateID, audit.OutcomeSuccess); err != nil {
			return err
		}

		row, err := q.GetTemplateByID(ctx, baseImg.TemplateID)
		if err != nil {
			return fmt.Errorf("reload template: %w", err)
		}
		result = toTemplate(row)
		return nil
	})
	if txErr != nil {
		return Template{}, txErr
	}
	return result, nil
}

// ListCandidateImages returns every base image ever captured for
// templateID, newest first — how an admin discovers candidate ids to
// promote.
func (s *Service) ListCandidateImages(ctx context.Context, templateID string) ([]BaseImage, error) {
	rows, err := s.q.ListBaseImagesByTemplate(ctx, templateID)
	if err != nil {
		return nil, fmt.Errorf("list base images: %w", err)
	}
	items := make([]BaseImage, 0, len(rows))
	for _, row := range rows {
		items = append(items, toBaseImage(row))
	}
	return items, nil
}

// ResolveCurrentBaseImage returns the artifact path a new instance of
// templateID should seed from, and ok=false if no template row exists yet
// or none has ever been promoted — the common case, meaning "clone from the
// live AVD, exactly as before this feature existed."
func (s *Service) ResolveCurrentBaseImage(ctx context.Context, templateID string) (artifactPath string, ok bool, err error) {
	tmpl, err := s.q.GetTemplateByID(ctx, templateID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("get template: %w", err)
	}
	if !tmpl.CurrentBaseImageID.Valid {
		return "", false, nil
	}

	baseImg, err := s.q.GetBaseImageByID(ctx, tmpl.CurrentBaseImageID.String)
	if err != nil {
		return "", false, fmt.Errorf("get current base image: %w", err)
	}
	return baseImg.ArtifactPath, true, nil
}
