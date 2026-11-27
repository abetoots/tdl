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

// pagination reference: https://github.com/telegramdesktop/tdesktop/blob/4047f1733decd5edf96d125589f128758b68d922/Telegram/SourceFiles/data/data_forum.cpp#L135
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

		for _, tp := range topics.Topics {
			if t, ok := tp.(*tg.ForumTopic); ok {
				res = append(res, ForumTopic{ID: t.ID, Title: t.Title})
				offsetTopic = t.ID
			}
		}

		if len(res) >= topics.Count || len(topics.Topics) < limit {
			break
		}

		msgCount := len(topics.Messages)
		if msgCount > 0 {
			if lastMsg, ok := topics.Messages[msgCount-1].AsNotEmpty(); ok {
				offsetID, offsetDate = lastMsg.GetID(), lastMsg.GetDate()
			} else {
				log.Debug("no valid message for offset, relying on offsetTopic only",
					zap.Int("offset_topic", offsetTopic))
			}
		} else {
			log.Debug("no messages in topics response, relying on offsetTopic only",
				zap.Int("offset_topic", offsetTopic),
				zap.Int("topics_count", len(topics.Topics)))
		}
	}

	return res, nil
}
