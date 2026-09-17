package events

import (
	"context"

	"github.com/discordia-grupo01/identify-service/internal/repository"
)

type membershipHandler struct {
	repo repository.ServerMembershipRepository
}

func NewMembershipHandler(repo repository.ServerMembershipRepository) MembershipHandler {
	return &membershipHandler{repo: repo}
}

func (h *membershipHandler) HandleMemberJoined(ctx context.Context, payload EventPayload) error {
	return h.repo.Upsert(ctx, payload.UserID, payload.ServerID, true, payload.OccurredAt)
}

func (h *membershipHandler) HandleMemberLeft(ctx context.Context, payload EventPayload) error {
	return h.repo.Upsert(ctx, payload.UserID, payload.ServerID, false, payload.OccurredAt)
}
