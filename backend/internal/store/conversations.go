package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/patrhez/agent-platform/backend/internal/domain"
	"github.com/patrhez/agent-platform/backend/internal/model"
	"github.com/patrhez/agent-platform/backend/internal/query"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateUserMessageAndRun appends a user Message and its queued Run atomically.
// When mode is steer, active Runs are cancelled first in the same transaction.
func (store *Store) CreateUserMessageAndRun(
	context context.Context,
	conversationID string,
	clientMessageID string,
	content string,
	mode domain.FollowUpMode,
	pinned domain.RunPins,
) (domain.Run, []domain.RunEvent, error) {
	queries := query.Use(store.database)
	var createdRun *model.Run
	var cancelEvents []domain.RunEvent
	err := queries.Transaction(func(transaction *query.Query) error {
		conversation, err := lockConversation(context, transaction, conversationID)
		if err != nil {
			return err
		}

		existingRun, found, err := findIdempotentRun(context, transaction, conversationID, clientMessageID)
		if err != nil {
			return err
		}
		if found {
			createdRun = existingRun
			return nil
		}

		if mode == domain.FollowUpModeSteer {
			cancelEvents, err = cancelActiveRunsInTx(context, transaction, conversation)
			if err != nil {
				return err
			}
		}

		createdRun, err = createMessageAndRun(
			context,
			transaction,
			conversation,
			conversationID,
			clientMessageID,
			content,
			pinned,
		)
		return err
	})
	if err != nil {
		return domain.Run{}, nil, fmt.Errorf("create user Message and Run: %w", err)
	}

	return domainRun(createdRun), cancelEvents, nil
}

func lockConversation(
	context context.Context,
	transaction *query.Query,
	conversationID string,
) (*model.Conversation, error) {
	conversation, err := transaction.Conversation.WithContext(context).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where(transaction.Conversation.ID.Eq(conversationID)).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrConversationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock Conversation %s: %w", conversationID, err)
	}
	return conversation, nil
}

func findIdempotentRun(
	context context.Context,
	transaction *query.Query,
	conversationID string,
	clientMessageID string,
) (*model.Run, bool, error) {
	message, err := transaction.Message.WithContext(context).
		Where(
			transaction.Message.ConversationID.Eq(conversationID),
			transaction.Message.ClientMessageID.Eq(clientMessageID),
		).
		First()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("find idempotent Message: %w", err)
	}

	run, err := transaction.Run.WithContext(context).
		Where(transaction.Run.TriggerMessageID.Eq(message.ID)).
		First()
	if err != nil {
		return nil, false, fmt.Errorf("find idempotent Run for Message %s: %w", message.ID, err)
	}
	return run, true, nil
}

func createMessageAndRun(
	context context.Context,
	transaction *query.Query,
	conversation *model.Conversation,
	conversationID string,
	clientMessageID string,
	content string,
	pinned domain.RunPins,
) (*model.Run, error) {
	now := time.Now().UTC()
	message := newUserMessage(conversationID, clientMessageID, content, conversation.NextMessageSeq, now)
	if err := transaction.Message.WithContext(context).Create(message); err != nil {
		return nil, fmt.Errorf("create user Message: %w", err)
	}

	run := newQueuedRun(conversationID, message.ID, conversation.NextRunSeq, pinned, now)
	if err := transaction.Run.WithContext(context).Create(run); err != nil {
		return nil, fmt.Errorf("create queued Run: %w", err)
	}
	if err := advanceConversation(context, transaction, conversation, now); err != nil {
		return nil, err
	}
	return run, nil
}

func newUserMessage(
	conversationID string,
	clientMessageID string,
	content string,
	sequence int64,
	now time.Time,
) *model.Message {
	return &model.Message{
		ID:              ulid.Make().String(),
		ConversationID:  conversationID,
		Seq:             sequence,
		Role:            "user",
		Content:         content,
		Status:          "final",
		ClientMessageID: &clientMessageID,
		CreatedAt:       now,
		FinalizedAt:     &now,
	}
}

func newQueuedRun(
	conversationID string,
	messageID string,
	sequence int64,
	pinned domain.RunPins,
	now time.Time,
) *model.Run {
	return &model.Run{
		ID:                  ulid.Make().String(),
		ConversationID:      conversationID,
		TriggerMessageID:    messageID,
		QueueSeq:            sequence,
		Status:              string(domain.RunStatusQueued),
		NextAttemptAt:       now,
		AgentConfigVersion:  pinned.AgentConfigVersion,
		SkillsBundleVersion: pinned.SkillsBundleVersion,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func advanceConversation(
	context context.Context,
	transaction *query.Query,
	conversation *model.Conversation,
	now time.Time,
) error {
	result, err := transaction.Conversation.WithContext(context).
		Where(transaction.Conversation.ID.Eq(conversation.ID)).
		UpdateSimple(
			transaction.Conversation.NextMessageSeq.Value(conversation.NextMessageSeq+1),
			transaction.Conversation.NextRunSeq.Value(conversation.NextRunSeq+1),
			transaction.Conversation.UpdatedAt.Value(now),
			transaction.Conversation.LatestMessageAt.Value(now),
		)
	if err != nil {
		return fmt.Errorf("advance Conversation %s: %w", conversation.ID, err)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("advance Conversation %s: expected one row, got %d", conversation.ID, result.RowsAffected)
	}
	return nil
}
