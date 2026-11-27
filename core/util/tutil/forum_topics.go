package tutil

import (
	"context"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/logctx"
)

type ForumTopic struct {
	ID    int
	Title string
}

// see https://core.telegram.org/constructor/messages.forumTopics for pagination.
func GetForumTopics(ctx context.Context, api *tg.Client, peer tg.InputPeerClass) ([]ForumTopic, error) {
	const limit = 100
	log := logctx.From(ctx)
	res := make([]ForumTopic, 0)
	offsetTopic, offsetID, offsetDate := 0, 0, 0
	lastOffsetTopic := -1
	seenOffsets := make(map[int]bool)

	for {
		if offsetTopic == lastOffsetTopic && lastOffsetTopic != -1 {
			log.Warn("pagination stuck (same offset), breaking loop",
				zap.Int("offset_topic", offsetTopic))
			break
		}
		if seenOffsets[offsetTopic] {
			log.Warn("pagination cycle detected, breaking loop",
				zap.Int("offset_topic", offsetTopic))
			break
		}
		seenOffsets[offsetTopic] = true
		lastOffsetTopic = offsetTopic

		req := &tg.MessagesGetForumTopicsRequest{
			Peer:        peer,
			Limit:       limit,
			OffsetTopic: offsetTopic,
			OffsetID:    offsetID,
			OffsetDate:  offsetDate,
		}

		topics, err := api.MessagesGetForumTopics(ctx, req)
		if err != nil {
			return nil, errors.Wrap(err, "get forum topics")
		}
		if len(topics.Topics) == 0 {
			break
		}

		var lastTopic *tg.ForumTopic
		for _, topic := range topics.Topics {
			if forumTopic, ok := topic.(*tg.ForumTopic); ok {
				res = append(res, ForumTopic{ID: forumTopic.ID, Title: forumTopic.Title})
				lastTopic = forumTopic
			}
		}

		if len(res) >= topics.Count {
			break
		}
		if lastTopic == nil {
			continue
		}
		offsetTopic = lastTopic.ID
		offsetID, offsetDate = 0, 0
		if topics.OrderByCreateDate {
			offsetDate = lastTopic.Date
			continue
		}
		for _, message := range topics.Messages {
			if msg, ok := message.AsNotEmpty(); ok && msg.GetID() == lastTopic.TopMessage {
				offsetID, offsetDate = msg.GetID(), msg.GetDate()
				break
			}
		}
	}

	return res, nil
}
