package transport

// Adapter abstracts the streaming protocol (WebRTC now, WebTransport later).
// One Adapter instance exists per active browser attachment.
type Adapter interface {
	// HandleOffer accepts an SDP offer and returns an SDP answer.
	// role is "owner" | "participant" | "observer".
	HandleOffer(instanceID, attachmentID, role, sdpOffer string) (sdpAnswer string, err error)

	// HandleICECandidate forwards a trickle-ICE candidate from the browser.
	HandleICECandidate(candidate string) error

	// HandleInput forwards a normalized input event to the runtime.
	// Observers are rejected by the adapter.
	HandleInput(ev InputEvent) error

	// SetRole changes the attachment's access role.
	SetRole(role string)

	// Role returns the attachment's current access role.
	Role() string

	// Stop tears down the peer connection and capture processes.
	Stop() error
}

// InputEvent carries a normalized input action from the browser.
type InputEvent struct {
	Type    string  `json:"type"`    // "touch" | "key"
	Action  string  `json:"action"`  // "down" | "up" | "move" (touch only)
	X       float64 `json:"x"`       // normalized 0–1 (touch only)
	Y       float64 `json:"y"`       // normalized 0–1 (touch only)
	Keycode int     `json:"keycode"` // Android keycode (key only)
}

// NoopAdapter is a do-nothing implementation used in tests.
type NoopAdapter struct{}

func (NoopAdapter) HandleOffer(_, _, _, _ string) (string, error) { return "sdp-answer", nil }
func (NoopAdapter) HandleICECandidate(_ string) error             { return nil }
func (NoopAdapter) HandleInput(_ InputEvent) error                { return nil }
func (NoopAdapter) SetRole(_ string)                              {}
func (NoopAdapter) Role() string                                  { return "" }
func (NoopAdapter) Stop() error                                   { return nil }
