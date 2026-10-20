// Package image implements the template and base-image version registry,
// and capture/promote orchestration (roadmap Phase 6, designed in
// docs/architecture/planned-subsystems.md's "Base image and user data
// model"). Capturing a candidate image never affects anything live;
// promoting one moves a template's "current" pointer, which is the only
// step that changes what future instances of that template boot from.
// Rollback is the same primitive as promotion: promote an older candidate.
package image
