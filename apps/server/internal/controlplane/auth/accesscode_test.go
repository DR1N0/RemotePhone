package auth

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAccessCodeCreatesThenPairsExistingUser(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	code, _, err := svc.IssueAccessCode(ctx, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("IssueAccessCode: %v", err)
	}
	user, token, created, err := svc.RedeemAccessCode(ctx, code, "new_user", "password123", "phone")
	if err != nil {
		t.Fatalf("RedeemAccessCode create: %v", err)
	}
	if !created || user.Username == nil || *user.Username != "new_user" || token == "" {
		t.Fatalf("unexpected create result: created=%v user=%+v token=%q", created, user, token)
	}

	code, _, err = svc.IssueAccessCode(ctx, nil, nil, time.Hour)
	if err != nil {
		t.Fatalf("IssueAccessCode 2: %v", err)
	}
	user, token, created, err = svc.RedeemAccessCode(ctx, code, "new_user", "password123", "tablet")
	if err != nil {
		t.Fatalf("RedeemAccessCode pair: %v", err)
	}
	if created || user.ID == "" || token == "" {
		t.Fatalf("unexpected pair result: created=%v user=%+v token=%q", created, user, token)
	}
}

func TestBoundAccessCodeRejectsOtherUsername(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	code, _, err := svc.IssueAccessCode(ctx, nil, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	user, _, _, err := svc.RedeemAccessCode(ctx, code, "bound_user", "password123", "phone")
	if err != nil {
		t.Fatal(err)
	}
	bound, _, err := svc.IssueAccessCode(ctx, &user.ID, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.RedeemAccessCode(ctx, bound, "someone_else", "password123", "phone"); !errors.Is(err, ErrAccessCodeInvalid) {
		t.Fatalf("bound code with other username = %v, want ErrAccessCodeInvalid", err)
	}
}

func TestAccessCodeIsSingleUse(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	code, _, err := svc.IssueAccessCode(ctx, nil, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.RedeemAccessCode(ctx, code, "once_user", "password123", "phone"); err != nil {
		t.Fatalf("first redemption: %v", err)
	}
	if _, _, _, err := svc.RedeemAccessCode(ctx, code, "once_user", "password123", "tablet"); !errors.Is(err, ErrAccessCodeUsed) {
		t.Fatalf("second redemption = %v, want ErrAccessCodeUsed", err)
	}
}

func TestAccessCodeWrongPasswordDoesNotConsumeOnFirstTry(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	code, _, err := svc.IssueAccessCode(ctx, nil, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.RedeemAccessCode(ctx, code, "retry_user", "password123", "phone"); err != nil {
		t.Fatal(err)
	}
	pairCode, _, err := svc.IssueAccessCode(ctx, nil, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.RedeemAccessCode(ctx, pairCode, "retry_user", "wrong-password", "tablet"); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("wrong password = %v, want ErrInvalidLogin", err)
	}
	if _, _, created, err := svc.RedeemAccessCode(ctx, pairCode, "retry_user", "password123", "tablet"); err != nil || created {
		t.Fatalf("retry after one failure: created=%v err=%v", created, err)
	}
}
