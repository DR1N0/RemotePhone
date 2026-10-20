package image

// BaseImage is a captured, immutable snapshot of one instance's AVD state
// at the moment of capture — inert until promoted.
type BaseImage struct {
	ID               string `json:"id"`
	TemplateID       string `json:"templateId"`
	SourceInstanceID string `json:"sourceInstanceId"`
	ArtifactPath     string `json:"artifactPath"`
	CreatedByUserID  string `json:"createdByUserId"`
	CreatedAt        string `json:"createdAt"`
}

// Template tracks which base image (if any) new instances of this
// TemplateID currently resolve to. A nil CurrentBaseImageID means no image
// has ever been promoted — new instances clone from the live, on-disk AVD
// exactly as before this feature existed.
type Template struct {
	ID                 string  `json:"id"`
	RuntimeClass       string  `json:"runtimeClass"`
	DisplayName        string  `json:"displayName"`
	CurrentBaseImageID *string `json:"currentBaseImageId,omitempty"`
	CreatedAt          string  `json:"createdAt"`
	UpdatedAt          string  `json:"updatedAt"`
}
